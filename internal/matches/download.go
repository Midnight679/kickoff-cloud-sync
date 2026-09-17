package matches

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/Midnight679/kickoff-cloud-sync/internal/httpclient"
)

// DownloadReplay pulls the replay bytes directly from the cloud URL
// returned alongside a match history entry (confirmed as
// MatchEntry.ReplayUrl against dank/rlapi's real source — see
// ARCHITECTURE.md). Works regardless of platform (Epic, Steam, or
// console-linked-to-Epic) since it doesn't touch the local filesystem
// at all until the temp file below is written for upload.
func DownloadReplay(replayURL string) (string, error) {
	if err := validateReplayURL(replayURL); err != nil {
		return "", fmt.Errorf("refusing to download replay: %w", err)
	}

	resp, err := httpclient.Client().Get(replayURL)
	if err != nil {
		return "", fmt.Errorf("downloading replay: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("replay download failed (%d) — the cloud copy may have expired", resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "replay-*.replay")
	if err != nil {
		return "", err
	}
	defer tmp.Close()

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		return "", err
	}

	return tmp.Name(), nil
}

// validateReplayURL rejects anything that isn't a plausible
// cloud-hosted URL. replayURL comes from Epic's match history API —
// a trusted first-party source, but not one worth trusting blindly to
// never hand back something unexpected. This is defense in depth
// against ever blindly fetching (and then uploading the response
// bytes of) a local or internal address.
//
// Psyonix's own replay-serving endpoint (api.rlpp.psynet.gg) is
// plain HTTP, confirmed against dank/rlapi's documented API — so
// http is accepted here alongside https. Rejecting a local/internal
// host, not the scheme, is what actually matters for the SSRF
// concern this guards against.
func validateReplayURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("expected an http or https URL, got %q", u.Scheme)
	}
	switch strings.ToLower(u.Hostname()) {
	case "", "localhost", "127.0.0.1", "::1":
		return fmt.Errorf("refusing to fetch from host %q", u.Hostname())
	}
	return nil
}
