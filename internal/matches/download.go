package matches

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/Midnight679/kickoff-cloud-sync/internal/httpclient"
)

// maxReplayBytes caps how much a single replay download can be. Real
// replay files are a few MB at most; this isn't tuned to that, it's
// just a generous ceiling against an unexpectedly huge or runaway
// response instead of an unbounded io.Copy.
const maxReplayBytes = 64 * 1024 * 1024 // 64MB

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

	// A dedicated client rather than the shared httpclient.Client():
	// CheckRedirect needs to re-run validateReplayURL against every
	// redirect hop, which is specific to this one call, not something
	// every outbound call in the app (ballchasing included) should
	// have applied to it. The timeout still tracks the user's setting.
	client := &http.Client{
		Timeout: httpclient.Client().Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if err := validateReplayURL(req.URL.String()); err != nil {
				return fmt.Errorf("refusing to follow redirect: %w", err)
			}
			return nil
		},
	}

	resp, err := client.Get(replayURL)
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

	if err := copyCapped(tmp, resp.Body); err != nil {
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("saving downloaded replay: %w", err)
	}

	return tmp.Name(), nil
}

// copyCapped copies src into dst, erroring out instead of silently
// truncating once more than maxReplayBytes would be written — a
// truncated replay file would just fail to parse on ballchasing's
// side instead of surfacing the real problem here. Reads one byte past
// the cap so it can tell "exactly at the cap" apart from "more than
// the cap was available."
func copyCapped(dst io.Writer, src io.Reader) error {
	n, err := io.Copy(dst, io.LimitReader(src, maxReplayBytes+1))
	if err != nil {
		return err
	}
	if n > maxReplayBytes {
		return fmt.Errorf("replay download exceeded %d bytes, refusing", maxReplayBytes)
	}
	return nil
}

// validateReplayURL rejects anything that isn't a plausible
// cloud-hosted URL. replayURL comes from Epic's match history API —
// a trusted first-party source, but not one worth trusting blindly to
// never hand back something unexpected. This is defense in depth
// against ever blindly fetching (and then uploading the response
// bytes of) a local or internal address — checked again on every
// redirect hop by DownloadReplay's CheckRedirect, not just the
// original URL.
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
	if isDisallowedReplayHost(u.Hostname()) {
		return fmt.Errorf("refusing to fetch from host %q", u.Hostname())
	}
	return nil
}

// isDisallowedReplayHost reports whether host is (or is a literal IP
// address for) a loopback, unspecified, private, or link-local
// address — every form of "this points somewhere internal" that a
// download from an untrusted URL needs to reject, not just the three
// specific spellings (localhost, 127.0.0.1, ::1) a hostname blocklist
// would otherwise name explicitly while missing the many equivalent
// ones (127.0.0.2, 0.0.0.0, 10.x, 172.16-31.x, 192.168.x, fe80::, …).
func isDisallowedReplayHost(host string) bool {
	if host == "" || strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false // a real DNS name — not an address literal, nothing more to check here
	}
	return ip.IsLoopback() || ip.IsUnspecified() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}
