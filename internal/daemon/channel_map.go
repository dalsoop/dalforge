package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ChannelMap manages pane↔MM channel 1:1 mappings.
// Persistence: /etc/dalcenter/channel-map.json (or stateDir fallback).
type ChannelMap struct {
	mmURL   string
	mmToken string
	mmTeam  string

	entries  map[string]*ChannelEntry // pane → entry
	mu       sync.RWMutex
	filePath string
}

// ChannelEntry holds one pane↔channel mapping.
type ChannelEntry struct {
	Pane        string    `json:"pane"`
	ChannelID   string    `json:"channel_id"`
	ChannelName string    `json:"channel_name"`
	CreatedAt   time.Time `json:"created_at"`
}

// newChannelMap creates a ChannelMap from environment.
func newChannelMap(serviceRepo string) *ChannelMap {
	mmURL := os.Getenv("DALCENTER_MM_URL")
	mmToken := os.Getenv("DALCENTER_MM_TOKEN")
	mmTeam := os.Getenv("DALCENTER_MM_TEAM")
	if mmTeam == "" {
		mmTeam = "dalsoop"
	}

	// Prefer /etc/dalcenter/channel-map.json if dir exists
	fp := filepath.Join("/etc/dalcenter", "channel-map.json")
	if _, err := os.Stat(filepath.Dir(fp)); err != nil {
		fp = filepath.Join(stateDir(serviceRepo), "channel-map.json")
	}

	cm := &ChannelMap{
		mmURL:    strings.TrimRight(mmURL, "/"),
		mmToken:  mmToken,
		mmTeam:   mmTeam,
		entries:  make(map[string]*ChannelEntry),
		filePath: fp,
	}
	cm.load()
	return cm
}

func (cm *ChannelMap) configured() bool {
	return cm.mmURL != "" && cm.mmToken != ""
}

// Create creates a new MM channel with the given name.
func (cm *ChannelMap) Create(name, purpose string) (*ChannelEntry, error) {
	if !cm.configured() {
		return nil, fmt.Errorf("MM not configured (DALCENTER_MM_URL/DALCENTER_MM_TOKEN)")
	}

	teamID, err := cm.getTeamID()
	if err != nil {
		return nil, fmt.Errorf("resolve team: %w", err)
	}

	// Check if channel already exists
	chID, err := cm.getChannelIDByName(teamID, name)
	if err != nil {
		// Create new channel
		chID, err = cm.createChannel(teamID, name, purpose)
		if err != nil {
			return nil, fmt.Errorf("create channel %s: %w", name, err)
		}
		log.Printf("[channel] created MM channel %s", name)
	} else {
		log.Printf("[channel] MM channel %s already exists", name)
	}

	return &ChannelEntry{
		ChannelID:   chID,
		ChannelName: name,
		CreatedAt:   time.Now(),
	}, nil
}

// Delete archives an MM channel.
func (cm *ChannelMap) Delete(name string) error {
	if !cm.configured() {
		return fmt.Errorf("MM not configured")
	}

	teamID, err := cm.getTeamID()
	if err != nil {
		return fmt.Errorf("resolve team: %w", err)
	}

	chID, err := cm.getChannelIDByName(teamID, name)
	if err != nil {
		return fmt.Errorf("channel %q not found: %w", name, err)
	}

	if err := cm.deleteChannel(chID); err != nil {
		return fmt.Errorf("delete channel %s: %w", name, err)
	}

	// Remove from mapping if mapped
	cm.mu.Lock()
	for pane, entry := range cm.entries {
		if entry.ChannelName == name {
			delete(cm.entries, pane)
		}
	}
	cm.mu.Unlock()
	cm.persist()

	log.Printf("[channel] deleted MM channel %s", name)
	return nil
}

// Map binds a pane to an existing MM channel.
func (cm *ChannelMap) Map(pane, channelName string) (*ChannelEntry, error) {
	if !cm.configured() {
		return nil, fmt.Errorf("MM not configured")
	}

	teamID, err := cm.getTeamID()
	if err != nil {
		return nil, fmt.Errorf("resolve team: %w", err)
	}

	chID, err := cm.getChannelIDByName(teamID, channelName)
	if err != nil {
		return nil, fmt.Errorf("channel %q not found: %w", channelName, err)
	}

	entry := &ChannelEntry{
		Pane:        pane,
		ChannelID:   chID,
		ChannelName: channelName,
		CreatedAt:   time.Now(),
	}

	cm.mu.Lock()
	cm.entries[pane] = entry
	cm.mu.Unlock()
	cm.persist()

	log.Printf("[channel] mapped pane %s → %s", pane, channelName)
	return entry, nil
}

// Unmap removes a pane→channel binding.
func (cm *ChannelMap) Unmap(pane string) error {
	cm.mu.Lock()
	_, ok := cm.entries[pane]
	if !ok {
		cm.mu.Unlock()
		return fmt.Errorf("pane %q not mapped", pane)
	}
	delete(cm.entries, pane)
	cm.mu.Unlock()
	cm.persist()

	log.Printf("[channel] unmapped pane %s", pane)
	return nil
}

// List returns all pane→channel entries.
func (cm *ChannelMap) List() []*ChannelEntry {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	result := make([]*ChannelEntry, 0, len(cm.entries))
	for _, e := range cm.entries {
		result = append(result, e)
	}
	return result
}

// Sync auto-maps dalroot panes using the dalroot-{s}-{w}-{p} naming pattern.
// For each pane, creates the MM channel if needed and registers the mapping.
func (cm *ChannelMap) Sync(panes []string) ([]*ChannelEntry, error) {
	if !cm.configured() {
		return nil, fmt.Errorf("MM not configured")
	}

	teamID, err := cm.getTeamID()
	if err != nil {
		return nil, fmt.Errorf("resolve team: %w", err)
	}

	var result []*ChannelEntry
	for _, pane := range panes {
		channelName := "dalroot-" + pane

		chID, err := cm.getChannelIDByName(teamID, channelName)
		if err != nil {
			chID, err = cm.createChannel(teamID, channelName, fmt.Sprintf("dalroot pane %s", pane))
			if err != nil {
				log.Printf("[channel] sync: failed to create %s: %v", channelName, err)
				continue
			}
			log.Printf("[channel] sync: created %s", channelName)
		}

		entry := &ChannelEntry{
			Pane:        pane,
			ChannelID:   chID,
			ChannelName: channelName,
			CreatedAt:   time.Now(),
		}

		cm.mu.Lock()
		cm.entries[pane] = entry
		cm.mu.Unlock()

		result = append(result, entry)
	}
	cm.persist()
	return result, nil
}

// --- MM API helpers ---

func (cm *ChannelMap) mmRequest(method, path string, body string) (*http.Response, error) {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, cm.mmURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cm.mmToken)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return (&http.Client{Timeout: 10 * time.Second}).Do(req)
}

func (cm *ChannelMap) getTeamID() (string, error) {
	resp, err := cm.mmRequest("GET", "/api/v4/teams/name/"+cm.mmTeam, "")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("team lookup %d: %s", resp.StatusCode, string(b))
	}
	var result struct {
		ID string `json:"id"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if result.ID == "" {
		return "", fmt.Errorf("team %q not found", cm.mmTeam)
	}
	return result.ID, nil
}

func (cm *ChannelMap) getChannelIDByName(teamID, name string) (string, error) {
	resp, err := cm.mmRequest("GET", fmt.Sprintf("/api/v4/teams/%s/channels/name/%s", teamID, name), "")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("channel %q not found", name)
	}
	var result struct {
		ID string `json:"id"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.ID, nil
}

func (cm *ChannelMap) createChannel(teamID, name, purpose string) (string, error) {
	body := fmt.Sprintf(`{"team_id":%q,"name":%q,"display_name":%q,"purpose":%q,"type":"O"}`,
		teamID, name, name, purpose)
	resp, err := cm.mmRequest("POST", "/api/v4/channels", body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create channel %d: %s", resp.StatusCode, string(b))
	}
	var result struct {
		ID string `json:"id"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.ID, nil
}

func (cm *ChannelMap) deleteChannel(channelID string) error {
	resp, err := cm.mmRequest("DELETE", "/api/v4/channels/"+channelID, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete channel %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// --- persistence ---

func (cm *ChannelMap) persist() {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	b, _ := json.MarshalIndent(cm.entries, "", "  ")
	tmp := cm.filePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		log.Printf("[channel] persist error: %v", err)
		return
	}
	os.Rename(tmp, cm.filePath)
}

func (cm *ChannelMap) load() {
	data, err := os.ReadFile(cm.filePath)
	if err != nil {
		return
	}
	cm.mu.Lock()
	defer cm.mu.Unlock()
	json.Unmarshal(data, &cm.entries)
}
