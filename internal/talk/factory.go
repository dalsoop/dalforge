package talk

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// FactoryTalk represents the talk block from .dal-template/dal.cue.
type FactoryTalk struct {
	Channel   string   `json:"channel"`
	Conductor string   `json:"conductor"`
	Dals      []string `json:"dals"`
}

// FactoryDal represents a dal definition from .dal-template/{name}/dal.cue.
type FactoryDal struct {
	Name   string `json:"name"`
	Role   string `json:"role"`
	Player string `json:"player"` // "claude" or "codex"
}

// LoadTalkConfig reads the talk block from .dal-template/dal.cue.
// Uses a simple key-value parser (not full CUE) for portability.
func LoadTalkConfig(dalTemplatePath string) (*FactoryTalk, error) {
	dalCuePath := filepath.Join(dalTemplatePath, "dal.cue")
	data, err := os.ReadFile(dalCuePath)
	if err != nil {
		return nil, fmt.Errorf("read dal.cue: %w", err)
	}

	content := string(data)
	talk := &FactoryTalk{}

	// Parse talk block
	inTalk := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "talk: {" {
			inTalk = true
			continue
		}
		if inTalk && trimmed == "}" {
			break
		}
		if !inTalk {
			continue
		}

		if strings.HasPrefix(trimmed, "channel:") {
			talk.Channel = extractQuotedValue(trimmed)
		} else if strings.HasPrefix(trimmed, "conductor:") {
			talk.Conductor = extractQuotedValue(trimmed)
		} else if strings.HasPrefix(trimmed, "dals:") {
			talk.Dals = extractStringList(trimmed)
		}
	}

	if talk.Channel == "" {
		return nil, fmt.Errorf("no talk.channel in %s", dalCuePath)
	}
	return talk, nil
}

// LoadFactoryDal reads a dal definition from .dal-template/{name}/dal.cue.
func LoadFactoryDal(dalTemplatePath, name string) (*FactoryDal, error) {
	dalCuePath := filepath.Join(dalTemplatePath, name, "dal.cue")
	data, err := os.ReadFile(dalCuePath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dalCuePath, err)
	}

	content := string(data)
	dal := &FactoryDal{Name: name}

	inDal := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "dal: {" {
			inDal = true
			continue
		}
		if inDal && trimmed == "}" {
			break
		}
		if !inDal {
			continue
		}
		if strings.HasPrefix(trimmed, "role:") {
			dal.Role = extractQuotedValue(trimmed)
		} else if strings.HasPrefix(trimmed, "player:") {
			dal.Player = extractQuotedValue(trimmed)
		}
	}

	return dal, nil
}

// SetupFromFactory reads .dal-template and creates all bots + channel.
func SetupFromFactory(dalTemplatePath, mmURL, adminLogin, adminPassword, team string) error {
	talkCfg, err := LoadTalkConfig(dalTemplatePath)
	if err != nil {
		return err
	}

	adminToken, err := GetAdminToken(mmURL, adminLogin, adminPassword)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}

	teamID, channelID, err := GetTeamAndChannel(mmURL, adminToken, team, talkCfg.Channel)
	if err != nil {
		return fmt.Errorf("channel: %w", err)
	}

	fmt.Printf("channel: %s (id: %s)\n", talkCfg.Channel, channelID)

	// Create conductor bot
	conductorName := "dal-leader"
	if talkCfg.Conductor != "" {
		conductorName = talkCfg.Conductor
	}
	bot, err := SetupBot(mmURL, adminToken, teamID, channelID, conductorName, "Dal Leader", "dal 오케스트레이터")
	if err != nil {
		log.Printf("conductor bot %q: %v (may already exist)", conductorName, err)
	} else {
		fmt.Printf("conductor: %s (token: %s)\n", bot.Username, bot.Token)
	}

	// Create dal bots
	for _, dalName := range talkCfg.Dals {
		dalDef, err := LoadFactoryDal(dalTemplatePath, dalName)
		if err != nil {
			log.Printf("skip %s: %v", dalName, err)
			continue
		}

		botUsername := "dal-" + dalName
		displayName := dalName
		description := dalDef.Role

		dalBot, err := SetupBot(mmURL, adminToken, teamID, channelID, botUsername, displayName, description)
		if err != nil {
			log.Printf("dal bot %q: %v (may already exist)", botUsername, err)
			continue
		}
		fmt.Printf("dal: %s (token: %s, player: %s)\n", dalBot.Username, dalBot.Token, dalDef.Player)
	}

	return nil
}

func extractQuotedValue(line string) string {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) < 2 {
		return ""
	}
	v := strings.TrimSpace(parts[1])
	v = strings.Trim(v, `"'`)
	return v
}

func extractStringList(line string) []string {
	// Parse: dals: ["marketing", "tech-writer", "ci-worker"]
	idx := strings.Index(line, "[")
	if idx < 0 {
		return nil
	}
	end := strings.Index(line, "]")
	if end < 0 {
		return nil
	}
	inner := line[idx : end+1]
	var list []string
	json.Unmarshal([]byte(inner), &list)
	return list
}
