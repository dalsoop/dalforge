package daemon

import (
	"encoding/json"
	"net/http"
)

func (d *Daemon) handleChannelCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		Purpose string `json:"purpose"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		http.Error(w, "name required", 400)
		return
	}
	entry, err := d.channelMap.Create(req.Name, req.Purpose)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	respondJSON(w, 200, entry)
}

func (d *Daemon) handleChannelDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		http.Error(w, "name required", 400)
		return
	}
	if err := d.channelMap.Delete(req.Name); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	respondJSON(w, 200, map[string]string{"status": "deleted", "channel": req.Name})
}

func (d *Daemon) handleChannelMap(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Pane    string `json:"pane"`
		Channel string `json:"channel"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Pane == "" || req.Channel == "" {
		http.Error(w, "pane and channel required", 400)
		return
	}
	entry, err := d.channelMap.Map(req.Pane, req.Channel)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	respondJSON(w, 200, entry)
}

func (d *Daemon) handleChannelUnmap(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Pane string `json:"pane"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Pane == "" {
		http.Error(w, "pane required", 400)
		return
	}
	if err := d.channelMap.Unmap(req.Pane); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	respondJSON(w, 200, map[string]string{"status": "unmapped", "pane": req.Pane})
}

func (d *Daemon) handleChannelList(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, 200, map[string]any{"channels": d.channelMap.List()})
}

func (d *Daemon) handleChannelSync(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Panes []string `json:"panes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Panes) == 0 {
		http.Error(w, "panes required", 400)
		return
	}
	entries, err := d.channelMap.Sync(req.Panes)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	respondJSON(w, 200, map[string]any{"synced": entries})
}
