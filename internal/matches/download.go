package matches

import (
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/Midnight679/kickoff-cloud-sync/internal/httpclient"
)

// DownloadReplay pulls the replay bytes directly from the cloud
// URL returned alongside a match history entry. This is the
// primary path — it works regardless of platform (Epic, Steam, or
// console-linked-to-Epic) since it doesn't touch the local
// filesystem at all until we write the temp file below for upload.
//
// TODO: confirm the actual field name on rlapi's MatchEntry struct
// once you have network access to check the godoc — rlapi-py's
// docs describe this as "match history with replay URLs" but the
// exact Go field name isn't confirmed yet. Likely something like
// `m.ReplayURL` or nested under a `Replay` sub-struct.
func DownloadReplay(replayURL string) (string, error) {
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
