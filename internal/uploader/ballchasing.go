package uploader

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Midnight679/kickoff-cloud-sync/internal/httpclient"
)

const uploadURL = "https://ballchasing.com/api/v2/upload"
const pingURL = "https://ballchasing.com/api/"
const replayURLBase = "https://ballchasing.com/api/replays/"

// ValidateToken pings ballchasing.com with the given token to
// confirm it's actually accepted before an account gets saved with
// it. This is what backs the "confirm" step in the add-account flow.
func ValidateToken(token string) error {
	req, err := http.NewRequest(http.MethodGet, pingURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", token)

	resp, err := httpclient.Client().Do(req)
	if err != nil {
		return fmt.Errorf("contacting ballchasing: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("ballchasing rejected this token (401 unauthorized)")
	}
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected response from ballchasing (%d): %s", resp.StatusCode, data)
	}
	return nil
}

type UploadResult struct {
	ID       string `json:"id"`
	Location string `json:"location"`
}

// UploadReplay POSTs a .replay file to ballchasing.gg.
// visibility is one of: "public", "unlisted", "private".
func UploadReplay(token, filePath, visibility string) (*UploadResult, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("opening replay file: %w", err)
	}
	defer f.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(part, f); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s?visibility=%s", uploadURL, visibility)
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := httpclient.Client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// 201 Created = new upload, 409 Conflict = duplicate (already uploaded)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ballchasing upload failed (%d): %s", resp.StatusCode, data)
	}

	var result UploadResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ReplayPlayer is one player in ReplayTeam.Players.
type ReplayPlayer struct {
	Name string `json:"name"`
}

// ReplayTeam is one side (blue/orange) of ReplayDetails. Name is
// "Blue"/"Orange" for a normal match, but a private match's host can
// rename teams in the lobby — when they have, this carries that
// custom name (e.g. "POLAR BEARS") instead.
type ReplayTeam struct {
	Name    string         `json:"name"`
	Goals   int            `json:"goals"`
	Players []ReplayPlayer `json:"players"`
}

// ReplayDetails is ballchasing's own authoritative parse of an
// uploaded replay — used to build a meaningful title (mode, teams,
// win/loss) without us having to independently decode Psyonix's
// internal playlist IDs, which ballchasing already does correctly.
type ReplayDetails struct {
	Status       string     `json:"status"` // "pending", "ok", or "failed"
	PlaylistName string     `json:"playlist_name"`
	Blue         ReplayTeam `json:"blue"`
	Orange       ReplayTeam `json:"orange"`
}

// GetReplay fetches ballchasing's parsed details for a replay.
func GetReplay(token, replayID string) (*ReplayDetails, error) {
	req, err := http.NewRequest(http.MethodGet, replayURLBase+replayID, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", token)

	resp, err := httpclient.Client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fetching replay details failed (%d): %s", resp.StatusCode, data)
	}

	var details ReplayDetails
	if err := json.NewDecoder(resp.Body).Decode(&details); err != nil {
		return nil, err
	}
	return &details, nil
}

// GetReplayWithRetry polls GetReplay a few times, since ballchasing
// parses an upload asynchronously and a replay fetched immediately
// after upload is often still "pending". Gives up after a handful of
// short waits rather than blocking indefinitely — a replay that's
// still pending after this just keeps its default title.
func GetReplayWithRetry(token, replayID string) (*ReplayDetails, error) {
	var last *ReplayDetails
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}
		last, err = GetReplay(token, replayID)
		if err != nil {
			return nil, err
		}
		if last.Status == "ok" {
			return last, nil
		}
	}
	return nil, fmt.Errorf("replay still %q after retries", last.Status)
}

// SetReplayTitle renames an already-uploaded replay.
func SetReplayTitle(token, replayID, title string) error {
	body, err := json.Marshal(map[string]string{"title": title})
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPatch, replayURLBase+replayID, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpclient.Client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("setting replay title failed (%d): %s", resp.StatusCode, data)
	}
	return nil
}

// BuildTitle constructs a descriptive replay title from ballchasing's
// own parsed details, rather than leaving the default (the uploaded
// file's bare name).
//
// For a private match, mode and win/loss are less meaningful than
// who was actually playing. If the lobby host renamed the teams
// (ballchasing reports something other than the "Blue"/"Orange"
// defaults), that custom name is used directly — e.g. "Private —
// POLAR BEARS vs WHISKER GOBLINS". Otherwise the title lists each
// side's roster instead: "Private — Alice, Bob vs Carol, Dave".
//
// Otherwise (a non-private match) it's "{Playlist} — Win"/"Loss",
// e.g. "Ranked Doubles — Win". viewerName (the uploading account's
// Epic/platform display name) is matched case-insensitively against
// ballchasing's own player names to figure out which side is "yours"
// — if it can't be found on either team, the title falls back to just
// the playlist name rather than guessing.
func BuildTitle(details *ReplayDetails, viewerName string) string {
	if strings.EqualFold(details.PlaylistName, "Private") {
		blueLabel := teamLabel(details.Blue, "Blue")
		orangeLabel := teamLabel(details.Orange, "Orange")
		return fmt.Sprintf("Private — %s vs %s", blueLabel, orangeLabel)
	}

	viewer, opponent, ok := findViewerTeam(details, viewerName)
	if !ok {
		return details.PlaylistName
	}

	switch {
	case viewer.Goals > opponent.Goals:
		return details.PlaylistName + " — Win"
	case viewer.Goals < opponent.Goals:
		return details.PlaylistName + " — Loss"
	default:
		return details.PlaylistName
	}
}

func teamRoster(t ReplayTeam) string {
	names := make([]string, len(t.Players))
	for i, p := range t.Players {
		names[i] = p.Name
	}
	return strings.Join(names, ", ")
}

// teamLabel returns the team's custom name if the host set one in a
// private match's lobby, or its player roster if not (defaultName is
// ballchasing's own generic name for this side — "Blue" or "Orange").
func teamLabel(t ReplayTeam, defaultName string) string {
	if t.Name != "" && !strings.EqualFold(t.Name, defaultName) {
		return t.Name
	}
	return teamRoster(t)
}

func findViewerTeam(details *ReplayDetails, viewerName string) (viewer, opponent ReplayTeam, ok bool) {
	if teamHasPlayer(details.Blue, viewerName) {
		return details.Blue, details.Orange, true
	}
	if teamHasPlayer(details.Orange, viewerName) {
		return details.Orange, details.Blue, true
	}
	return ReplayTeam{}, ReplayTeam{}, false
}

func teamHasPlayer(t ReplayTeam, name string) bool {
	for _, p := range t.Players {
		if strings.EqualFold(p.Name, name) {
			return true
		}
	}
	return false
}
