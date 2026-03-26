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

	// Reuse existing token if available, otherwise create new one
	var token, tokenID string
	existingTokens, _ := mmAPI("GET", mmURL+"/api/v4/users/"+userID+"/tokens", adminToken, "")
	if existingTokens != nil {
		var tokens []map[string]interface{}
		if json.Unmarshal(existingTokens, &tokens) == nil && len(tokens) > 0 {
			// Reuse first active token's ID (can't read the token value, need new one)
			// But if we already have too many tokens, revoke old ones first
			for i, t := range tokens {
				if i >= 2 { // keep max 2 tokens, revoke older ones
					if tid, ok := t["id"].(string); ok {
						mmAPI("POST", mmURL+"/api/v4/users/"+userID+"/tokens/revoke", adminToken,
							fmt.Sprintf(`{"token_id":%q}`, tid))
					}
				}
			}
		}
	}
	tokenResp, err := mmAPI("POST", mmURL+"/api/v4/users/"+userID+"/tokens", adminToken,
		fmt.Sprintf(`{"description":%q}`, username+" token"))
	if err != nil {
		return nil, fmt.Errorf("create token: %w", err)
	}
	token = jsonStr(tokenResp, "token")
	tokenID = jsonStr(tokenResp, "id")

	// Add to team
	_, err = mmAPI("POST", mmURL+"/api/v4/teams/"+teamID+"/members", adminToken,
		fmt.Sprintf(`{"team_id":%q,"user_id":%q}`, teamID, userID))
	if err != nil {
		return nil, fmt.Errorf("add to team: %w", err)
	}

	// Add to channel
	_, err = mmAPI("POST", mmURL+"/api/v4/channels/"+channelID+"/members", adminToken,
		fmt.Sprintf(`{"user_id":%q}`, userID))
	if err != nil {
		return nil, fmt.Errorf("add to channel: %w", err)
	}

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

	// Get teams
	teamsResp, err := mmAPI("GET", mmURL+"/api/v4/teams", token, "")
	if err != nil {
		return "", "", fmt.Errorf("list teams: %w", err)
	}
	var teams []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(teamsResp, &teams); err != nil {
		return "", "", err
	}
	for _, t := range teams {
		if teamName == "" || t.Name == teamName {
			teamID = t.ID
			break
		}
	}
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
