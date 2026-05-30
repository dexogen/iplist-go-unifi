package sync

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type routeState struct {
	RouteID   string `json:"route_id"`
	Hash      string `json:"hash"`
	UpdatedAt string `json:"updated_at"`
}

type stateFile struct {
	Routes map[string]routeState `json:"routes"`
}

func loadState(path string) (stateFile, error) {
	state := stateFile{Routes: map[string]routeState{}}
	if path == "" {
		return state, nil
	}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return stateFile{}, err
	}
	if len(body) == 0 {
		return state, nil
	}
	if err := json.Unmarshal(body, &state); err != nil {
		return stateFile{}, err
	}
	if state.Routes == nil {
		state.Routes = map[string]routeState{}
	}
	return state, nil
}

func saveState(path string, state stateFile) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func setRouteState(state *stateFile, sourceName, routeID, hash string) {
	if state.Routes == nil {
		state.Routes = map[string]routeState{}
	}
	state.Routes[sourceName] = routeState{
		RouteID:   routeID,
		Hash:      hash,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
}
