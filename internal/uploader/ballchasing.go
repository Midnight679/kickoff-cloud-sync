package uploader

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/Midnight679/kickoff-cloud-sync/internal/httpclient"
)

const uploadURL = "https://ballchasing.com/api/v2/upload"
const pingURL = "https://ballchasing.com/api/"
const replayURLBase = "https://ballchasing.com/api/replays/"
const groupsURL = "https://ballchasing.com/api/groups"

// ballchasingLimiter throttles every outbound call to ballchasing.com
// to comfortably stay under their documented rate limit for accounts
// without a Patreon tier — 2 calls/second on the endpoints this app
// actually uses (confirmed against https://ballchasing.com/doc/api).
// Set below that so ordinary timing jitter never tips it over, and
// burst 1 so calls are paced rather than allowed to fire in a batch.
var ballchasingLimiter = rate.NewLimiter(rate.Limit(1.5), 1)

// doWithRetry runs one HTTP round-trip through the shared rate
// limiter, retrying with backoff if ballchasing responds 429 — their
// documented signal to "cool down for a bit before retrying." newReq
// builds a fresh *http.Request per attempt (rather than taking a
// pre-built one) since a request with a body can only be sent once.
func doWithRetry(newReq func() (*http.Request, error)) (*http.Response, error) {
	const maxAttempts = 4
	backoff := 2 * time.Second

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(backoff)
			backoff *= 2
		}

		if err := ballchasingLimiter.Wait(context.Background()); err != nil {
			return nil, err
		}

		req, err := newReq()
		if err != nil {
			return nil, err
		}
		resp, err := httpclient.Client().Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusTooManyRequests {
			return resp, nil
		}
		resp.Body.Close()
	}
	return nil, fmt.Errorf("ballchasing rate limit (429) persisted after %d attempts", maxAttempts)
}

// ValidateToken pings ballchasing.com with the given token to
// confirm it's actually accepted before an account gets saved with
// it. This is what backs the "confirm" step in the add-account flow.
func ValidateToken(token string) error {
	resp, err := doWithRetry(func() (*http.Request, error) {
		req, err := http.NewRequest(http.MethodGet, pingURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", token)
		return req, nil
	})
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

// UploadError is returned by UploadReplay for any response other than
// 201 (created) or 409 (duplicate, treated as success by the caller).
// StatusCode and Body carry ballchasing's actual response through
// rather than just a formatted string, so a caller can classify the
// failure (see Permanent) instead of just logging it.
type UploadError struct {
	StatusCode int
	Body       string
}

func (e *UploadError) Error() string {
	return fmt.Sprintf("ballchasing upload failed (%d): %s", e.StatusCode, e.Body)
}

// Permanent reports whether this is a rejection that retrying the same
// file will never fix: any 4xx other than 401/403 (the account's
// token is the problem, not the file — a new token can fix it, so
// it's worth trying again) and 429 (ballchasing's own documented
// "cool down and retry" signal, and in practice already retried
// several times by doWithRetry before this is ever returned). A 5xx,
// or a transport-level failure that never reaches this type at all,
// is always temporary.
func (e *UploadError) Permanent() bool {
	return e.StatusCode >= 400 && e.StatusCode < 500 &&
		e.StatusCode != http.StatusUnauthorized &&
		e.StatusCode != http.StatusForbidden &&
		e.StatusCode != http.StatusTooManyRequests
}

// UploadReplay POSTs a .replay file to ballchasing.gg.
// visibility is one of: "public", "unlisted", "private".
func UploadReplay(token, filePath, visibility string) (*UploadResult, error) {
	url := fmt.Sprintf("%s?visibility=%s", uploadURL, visibility)

	resp, err := doWithRetry(func() (*http.Request, error) {
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

		req, err := http.NewRequest(http.MethodPost, url, body)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", token)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		return req, nil
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// 201 Created = new upload, 409 Conflict = duplicate (already uploaded)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
		data, _ := io.ReadAll(resp.Body)
		return nil, &UploadError{StatusCode: resp.StatusCode, Body: string(data)}
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
	resp, err := doWithRetry(func() (*http.Request, error) {
		req, err := http.NewRequest(http.MethodGet, replayURLBase+replayID, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", token)
		return req, nil
	})
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

	resp, err := doWithRetry(func() (*http.Request, error) {
		req, err := http.NewRequest(http.MethodPatch, replayURLBase+replayID, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", token)
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	})
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

// CreateGroupResult is ballchasing's response to a successful
// POST /groups.
type CreateGroupResult struct {
	ID   string `json:"id"`
	Link string `json:"link"`
}

// CreateGroup creates a new top-level replay group (see the scrim-
// series grouping feature in internal/accounts, the only caller).
// player_identification is fixed to "by-id" since every replay in a
// group comes from the same uploading account, and
// team_identification to "by-player-clusters" so a mid-session sub
// doesn't split the series into two groups.
func CreateGroup(token, name string) (*CreateGroupResult, error) {
	body, err := json.Marshal(map[string]string{
		"name":                  name,
		"player_identification": "by-id",
		"team_identification":   "by-player-clusters",
	})
	if err != nil {
		return nil, err
	}

	resp, err := doWithRetry(func() (*http.Request, error) {
		req, err := http.NewRequest(http.MethodPost, groupsURL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", token)
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("creating ballchasing group failed (%d): %s", resp.StatusCode, data)
	}

	var result CreateGroupResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SetReplayGroup assigns an already-uploaded replay to groupID, or
// removes it from whatever group it's in if groupID is "".
func SetReplayGroup(token, replayID, groupID string) error {
	body, err := json.Marshal(map[string]string{"group": groupID})
	if err != nil {
		return err
	}

	resp, err := doWithRetry(func() (*http.Request, error) {
		req, err := http.NewRequest(http.MethodPatch, replayURLBase+replayID, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", token)
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("setting replay group failed (%d): %s", resp.StatusCode, data)
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
	if name, ok := customTeamName(t, defaultName); ok {
		return name
	}
	return teamRoster(t)
}

// customTeamName returns the lobby host's custom name for this side
// and ok=true, or ok=false if they left it on ballchasing's own
// generic default ("Blue"/"Orange", passed as defaultName).
func customTeamName(t ReplayTeam, defaultName string) (name string, ok bool) {
	if t.Name != "" && !strings.EqualFold(t.Name, defaultName) {
		return t.Name, true
	}
	return "", false
}

// CustomTeamPair returns the two custom team names of a private
// match, sorted so that the teams swapping blue/orange sides between
// games in the same series doesn't look like a different matchup. ok
// is false for anything that isn't a private match with both sides
// custom-named — a normal match, or a private match where the host
// didn't rename one or both teams, has no reliable "matchup identity"
// to group on.
func CustomTeamPair(details *ReplayDetails) (pair [2]string, ok bool) {
	if !strings.EqualFold(details.PlaylistName, "Private") {
		return pair, false
	}
	blue, blueOK := customTeamName(details.Blue, "Blue")
	orange, orangeOK := customTeamName(details.Orange, "Orange")
	if !blueOK || !orangeOK {
		return pair, false
	}
	pair = [2]string{blue, orange}
	sort.Strings(pair[:])
	return pair, true
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
