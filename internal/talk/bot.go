package talk

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// BotInfo holds Mattermost bot account details.
type BotInfo struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Token    string `json:"token"`
	TokenID  string `json:"token_id"`
}

// SetupBot creates a Mattermost bot account (or reuses existing), generates a token, and adds it to the team and channel.
func SetupBot(mmURL, adminToken, teamID, channelID, username, displayName, description string) (*BotInfo, error) {
	mmURL = strings.TrimRight(mmURL, "/")

	// Try to create bot
	botBody := fmt.Sprintf(`{"username":%q,"display_name":%q,"description":%q}`,
		username, displayName, description)
	botResp, err := mmAPI("POST", mmURL+"/api/v4/bots", adminToken, botBody)

	var userID string
	if err != nil {
		// Bot may already exist — try to find it
		userID = findExistingBotUserID(mmURL, adminToken, username)
		if userID == "" {
			return nil, fmt.Errorf("create bot: %w (and existing bot not found)", err)
		}
		// Re-enable if disabled
		mmAPI("POST", mmURL+"/api/v4/bots/"+userID+"/enable", adminToken, "")
	} else {
		userID = jsonStr(botResp, "user_id")
		if userID == "" {
			return nil, fmt.Errorf("create bot: no user_id in response: %s", string(botResp))
		}
	}

	// Revoke all existing tokens, then create exactly one new token.
	// keep max 2 tokens: old accumulation must never grow unbounded across wake cycles.
	existingTokens, _ := mmAPI("GET", mmURL+"/api/v4/users/"+userID+"/tokens", adminToken, "")
	if existingTokens != nil {
		var tokens []struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(existingTokens, &tokens) == nil {
			for _, t := range tokens {
				mmAPI("POST", mmURL+"/api/v4/users/"+userID+"/tokens/revoke", adminToken,
					fmt.Sprintf(`{"token_id":%q}`, t.ID))
			}
		}
	}
	tokenResp, err := mmAPI("POST", mmURL+"/api/v4/users/"+userID+"/tokens", adminToken,
		fmt.Sprintf(`{"description":%q}`, username+" token"))
	if err != nil {
		return nil, fmt.Errorf("create token: %w", err)
	}
	token := jsonStr(tokenResp, "token")
	tokenID := jsonStr(tokenResp, "id")

	// Add to team (ignore "already a member" errors for reused bots)
	_, err = mmAPI("POST", mmURL+"/api/v4/teams/"+teamID+"/members", adminToken,
		fmt.Sprintf(`{"team_id":%q,"user_id":%q}`, teamID, userID))
	if err != nil && !strings.Contains(err.Error(), "already") {
		return nil, fmt.Errorf("add to team: %w", err)
	}

	// Add to channel (ignore "already a member" errors for reused bots)
	_, err = mmAPI("POST", mmURL+"/api/v4/channels/"+channelID+"/members", adminToken,
		fmt.Sprintf(`{"user_id":%q}`, userID))
	if err != nil && !strings.Contains(err.Error(), "already") {
		return nil, fmt.Errorf("add to channel: %w", err)
	}

	// Clean up bot welcome DM ("Please add me to teams and channels...")
	// Mattermost sends this DM to all team members when a bot is created/enabled.
	cleanupBotWelcomeDMs(mmURL, adminToken, userID)

	return &BotInfo{
		UserID:   userID,
		Username: username,
		Token:    token,
		TokenID:  tokenID,
	}, nil
}

// TeardownBot disables a Mattermost bot and revokes its token.
func TeardownBot(mmURL, adminToken, username string) error {
	mmURL = strings.TrimRight(mmURL, "/")

	// Find bot by username
	botsResp, err := mmAPI("GET", mmURL+"/api/v4/bots?per_page=200", adminToken, "")
	if err != nil {
		return fmt.Errorf("list bots: %w", err)
	}

	var bots []struct {
		UserID   string `json:"user_id"`
		Username string `json:"username"`
	}
	if err := json.Unmarshal(botsResp, &bots); err != nil {
		return fmt.Errorf("parse bots: %w", err)
	}

	var userID string
	for _, b := range bots {
		if b.Username == username {
			userID = b.UserID
			break
		}
	}
	if userID == "" {
		return fmt.Errorf("bot %q not found", username)
	}

	// Revoke all tokens
	tokensResp, err := mmAPI("GET", mmURL+"/api/v4/users/"+userID+"/tokens?per_page=200", adminToken, "")
	if err == nil {
		var tokens []struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(tokensResp, &tokens) == nil {
			for _, t := range tokens {
				mmAPI("POST", mmURL+"/api/v4/users/"+userID+"/tokens/revoke", adminToken,
					fmt.Sprintf(`{"token_id":%q}`, t.ID))
			}
		}
	}

	// Disable bot
	_, err = mmAPI("POST", mmURL+"/api/v4/bots/"+userID+"/disable", adminToken, "")
	if err != nil {
		return fmt.Errorf("disable bot: %w", err)
	}

	return nil
}

// DeleteBot permanently deletes a disabled Mattermost bot. Used for GC of session bots.
func DeleteBot(mmURL, adminToken, userID string) error {
	mmURL = strings.TrimRight(mmURL, "/")
	_, err := mmAPI("POST", mmURL+"/api/v4/bots/"+userID+"/disable", adminToken, "")
	if err != nil {
		return fmt.Errorf("disable bot before delete: %w", err)
	}
	// Mattermost doesn't have a hard-delete API for bots, but disabling + revoking tokens
	// effectively removes the bot from the system. The disabled bot entry remains but is inert.
	return nil
}

// CleanupStaleBots finds disabled dal-* bots and deletes them.
// Called periodically by the daemon for GC.
func CleanupStaleBots(mmURL, adminToken string) int {
	mmURL = strings.TrimRight(mmURL, "/")
	// List disabled bots
	resp, err := mmAPI("GET", mmURL+"/api/v4/bots?per_page=200&include_deleted=true", adminToken, "")
	if err != nil {
		return 0
	}
	var bots []struct {
		UserID   string `json:"user_id"`
		Username string `json:"username"`
		DeleteAt int64  `json:"delete_at"`
	}
	if json.Unmarshal(resp, &bots) != nil {
		return 0
	}
	cleaned := 0
	for _, b := range bots {
		// Only clean dal-* session bots that are disabled (delete_at > 0)
		if strings.HasPrefix(b.Username, "dal-") && b.DeleteAt > 0 {
			// Revoke any remaining tokens
			tokResp, err := mmAPI("GET", mmURL+"/api/v4/users/"+b.UserID+"/tokens?per_page=200", adminToken, "")
			if err == nil {
				var tokens []struct{ ID string `json:"id"` }
				if json.Unmarshal(tokResp, &tokens) == nil {
					for _, t := range tokens {
						mmAPI("POST", mmURL+"/api/v4/users/"+b.UserID+"/tokens/revoke", adminToken,
							fmt.Sprintf(`{"token_id":%q}`, t.ID))
					}
				}
			}
			cleaned++
		}
	}
	return cleaned
}

// GetAdminToken logs in to Mattermost and returns a session token.
func GetAdminToken(mmURL, loginID, password string) (string, error) {
	mmURL = strings.TrimRight(mmURL, "/")
	body := fmt.Sprintf(`{"login_id":%q,"password":%q}`, loginID, password)

	req, _ := http.NewRequest("POST", mmURL+"/api/v4/users/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("login failed: %d %s", resp.StatusCode, string(b))
	}
	return resp.Header.Get("Token"), nil
}

// GetTeamAndChannel resolves team ID and channel ID by name.
// If the channel doesn't exist, it creates one.
func GetTeamAndChannel(mmURL, token, teamName, channelName string) (teamID, channelID string, err error) {
	mmURL = strings.TrimRight(mmURL, "/")

	// Get team by name (direct lookup works with bot tokens)
	if teamName == "" {
		return "", "", fmt.Errorf("team name is required")
	}
	teamResp, err := mmAPI("GET", mmURL+"/api/v4/teams/name/"+teamName, token, "")
	if err != nil {
		return "", "", fmt.Errorf("get team %q: %w", teamName, err)
	}
	teamID = jsonStr(teamResp, "id")
	if teamID == "" {
		return "", "", fmt.Errorf("team %q not found", teamName)
	}

	// Try to get existing channel
	chResp, err := mmAPI("GET", mmURL+"/api/v4/teams/"+teamID+"/channels/name/"+channelName, token, "")
	if err == nil {
		channelID = jsonStr(chResp, "id")
		if channelID != "" {
			return teamID, channelID, nil
		}
	}

	// Channel not found — create it
	channelID, err = CreateChannel(mmURL, token, teamID, channelName)
	if err != nil {
		return teamID, "", err
	}
	return teamID, channelID, nil
}

// CreateChannel creates a public Mattermost channel, or restores it if archived.
func CreateChannel(mmURL, token, teamID, channelName string) (string, error) {
	mmURL = strings.TrimRight(mmURL, "/")

	// Try create
	body := fmt.Sprintf(`{"team_id":%q,"name":%q,"display_name":%q,"type":"O","purpose":"dalcenter talk channel"}`,
		teamID, channelName, channelName)
	resp, err := mmAPI("POST", mmURL+"/api/v4/channels", token, body)
	if err == nil {
		id := jsonStr(resp, "id")
		if id != "" {
			return id, nil
		}
	}

	// If exists (possibly archived), search and restore
	searchBody := fmt.Sprintf(`{"term":%q,"include_deleted":true}`, channelName)
	searchResp, err := mmAPI("POST", mmURL+"/api/v4/channels/search", token, searchBody)
	if err != nil {
		return "", fmt.Errorf("search channel %q: %w", channelName, err)
	}
	var channels []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		DeleteAt int64  `json:"delete_at"`
	}
	if err := json.Unmarshal(searchResp, &channels); err != nil {
		return "", fmt.Errorf("parse search: %w", err)
	}
	for _, ch := range channels {
		if ch.Name == channelName {
			if ch.DeleteAt > 0 {
				// Restore archived channel
				_, err := mmAPI("POST", mmURL+"/api/v4/channels/"+ch.ID+"/restore", token, "")
				if err != nil {
					return "", fmt.Errorf("restore channel %q: %w", channelName, err)
				}
			}
			return ch.ID, nil
		}
	}
	return "", fmt.Errorf("channel %q: create and search both failed", channelName)
}

// DeleteChannel archives a Mattermost channel.
func DeleteChannel(mmURL, token, channelID string) error {
	mmURL = strings.TrimRight(mmURL, "/")
	_, err := mmAPI("DELETE", mmURL+"/api/v4/channels/"+channelID, token, "")
	return err
}

// FindOrCreateChannel finds an existing channel by name, or creates it.
func FindOrCreateChannel(mmURL, adminToken, teamID, channelName string) (string, error) {
	mmURL = strings.TrimRight(mmURL, "/")
	resp, err := mmAPI("GET", mmURL+"/api/v4/teams/"+teamID+"/channels/name/"+channelName, adminToken, "")
	if err == nil {
		if id := jsonStr(resp, "id"); id != "" {
			return id, nil
		}
	}
	return CreateChannel(mmURL, adminToken, teamID, channelName)
}

// AddUserToChannel adds a user to a Mattermost channel.
func AddUserToChannel(mmURL, adminToken, channelID, userID string) error {
	mmURL = strings.TrimRight(mmURL, "/")
	body := fmt.Sprintf(`{"user_id":%q}`, userID)
	_, err := mmAPI("POST", mmURL+"/api/v4/channels/"+channelID+"/members", adminToken, body)
	return err
}

func mmAPI(method, url, token, body string) ([]byte, error) {
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, url, bodyReader)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%d: %s", resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// findExistingBotUserID looks up an existing bot by username, including disabled bots.
func findExistingBotUserID(mmURL, adminToken, username string) string {
	resp, err := mmAPI("GET", mmURL+"/api/v4/bots?per_page=200&include_deleted=true", adminToken, "")
	if err != nil {
		return ""
	}
	var bots []struct {
		UserID   string `json:"user_id"`
		Username string `json:"username"`
	}
	if json.Unmarshal(resp, &bots) != nil {
		return ""
	}
	for _, b := range bots {
		if b.Username == username {
			return b.UserID
		}
	}
	return ""
}

func jsonStr(data []byte, key string) string {
	var m map[string]interface{}
	if json.Unmarshal(data, &m) != nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// RemoveBotFromChannel removes a bot user from a Mattermost channel.
func RemoveBotFromChannel(mmURL, adminToken, channelID, botUsername string) {
	mmURL = strings.TrimRight(mmURL, "/")
	userID := findExistingBotUserID(mmURL, adminToken, botUsername)
	if userID == "" {
		return
	}
	mmAPI("DELETE", mmURL+"/api/v4/channels/"+channelID+"/members/"+userID, adminToken, "")
}

// cleanupBotWelcomeDMs deletes the "Please add me to teams and channels" DM
// that Mattermost auto-sends when a bot is created or enabled.
func cleanupBotWelcomeDMs(mmURL, adminToken, botUserID string) {
	// Get DM channels for this bot
	data, err := mmAPI("GET", mmURL+"/api/v4/users/"+botUserID+"/channels", adminToken, "")
	if err != nil {
		return
	}
	var channels []map[string]interface{}
	if json.Unmarshal(data, &channels) != nil {
		return
	}
	for _, ch := range channels {
		chType, _ := ch["type"].(string)
		if chType != "D" {
			continue
		}
		chID, _ := ch["id"].(string)
		if chID == "" {
			continue
		}
		// Get recent posts in this DM and delete bot's "Please add me" messages
		postsData, err := mmAPI("GET", mmURL+"/api/v4/channels/"+chID+"/posts?per_page=5", adminToken, "")
		if err != nil {
			continue
		}
		var posts map[string]interface{}
		if json.Unmarshal(postsData, &posts) != nil {
			continue
		}
		order, _ := posts["order"].([]interface{})
		postsMap, _ := posts["posts"].(map[string]interface{})
		for _, idRaw := range order {
			id, _ := idRaw.(string)
			post, _ := postsMap[id].(map[string]interface{})
			if post == nil {
				continue
			}
			uid, _ := post["user_id"].(string)
			msg, _ := post["message"].(string)
			if uid == botUserID && strings.Contains(msg, "Please add me to teams") {
				mmAPI("DELETE", mmURL+"/api/v4/posts/"+id, adminToken, "")
			}
		}
	}
}

// HideBotDMFromUsers hides the bot's DM channel from all team members' sidebars.
// Called on sleep to keep the DM list clean.
func HideBotDMFromUsers(mmURL, adminToken, teamID, botUsername string) {
	mmURL = strings.TrimRight(mmURL, "/")
	botUserID := findExistingBotUserID(mmURL, adminToken, botUsername)
	if botUserID == "" {
		return
	}
	// Get team members
	data, _ := mmAPI("GET", mmURL+"/api/v4/teams/"+teamID+"/members?per_page=200", adminToken, "")
	if data == nil {
		return
	}
	var members []map[string]interface{}
	if json.Unmarshal(data, &members) != nil {
		return
	}
	for _, m := range members {
		uid, _ := m["user_id"].(string)
		if uid == "" || uid == botUserID {
			continue
		}
		// Hide bot DM from this user's sidebar
		pref := fmt.Sprintf(`[{"user_id":%q,"category":"direct_channel_show","name":%q,"value":"false"}]`, uid, botUserID)
		mmAPI("PUT", mmURL+"/api/v4/users/"+uid+"/preferences", adminToken, pref)
	}
}

// CleanupOrphanBotDMs hides DMs from disabled/orphan bots for all team members.
// Called on dalcenter serve startup.
func CleanupOrphanBotDMs(mmURL, adminToken, teamID string, activeBotUsernames []string) {
	mmURL = strings.TrimRight(mmURL, "/")
	// Get all bots (including disabled)
	data, _ := mmAPI("GET", mmURL+"/api/v4/bots?per_page=200&include_deleted=true", adminToken, "")
	if data == nil {
		return
	}
	var bots []map[string]interface{}
	if json.Unmarshal(data, &bots) != nil {
		return
	}
	activeSet := make(map[string]bool)
	for _, name := range activeBotUsernames {
		activeSet[name] = true
	}
	for _, bot := range bots {
		username, _ := bot["username"].(string)
		if activeSet[username] {
			continue // skip active bots
		}
		// Skip system bots
		if username == "feedbackbot" || username == "playbooks" || username == "calls" || username == "system-bot" {
			continue
		}
		userID, _ := bot["user_id"].(string)
		if userID == "" {
			continue
		}
		// Disable bot if not already
		deleteAt, _ := bot["delete_at"].(float64)
		if deleteAt == 0 {
			mmAPI("POST", mmURL+"/api/v4/bots/"+userID+"/disable", adminToken, "")
		}
		// Hide DM from all team members
		HideBotDMFromUsers(mmURL, adminToken, teamID, username)
	}
}
