package main

import (
	"log"
	"os"

	"github.com/Midnight679/kickoff-cloud-sync/internal/config"
	"github.com/Midnight679/kickoff-cloud-sync/internal/secrets"
)

// purgeAllData deletes every credential this app has ever stored —
// one per configured account, filed under the "kickoff-cloud-sync"
// keyring service (see internal/secrets), which cannot address any
// other application's credentials — and this app's entire
// %APPDATA%\kickoff-cloud-sync directory (config.json, the
// pending-uploads cache). Nothing else is ever touched: the only
// filesystem path involved is config.AppDataDir(), which is
// safety-checked to only ever resolve to that one dedicated
// subfolder, never anything broader.
//
// Invoked via the `--purge` command-line flag, which only the
// uninstaller is meant to pass, and only when the user opts in to
// removing local data. Runs to completion and exits without ever
// starting the GUI, tray, or a poll cycle.
func purgeAllData() {
	log.SetOutput(os.Stderr) // no logbuf needed for a one-shot CLI run

	cfg, err := config.Load()
	if err != nil {
		log.Printf("purge: failed to load config, skipping credential cleanup: %v", err)
	} else {
		for _, acct := range cfg.Accounts {
			if err := secrets.Delete(acct.ID); err != nil {
				log.Printf("purge: failed to delete credentials for account %s: %v", acct.ID, err)
			}
		}
	}

	appDir, err := config.AppDataDir()
	if err != nil {
		log.Printf("purge: could not safely locate app data directory, nothing removed: %v", err)
		return
	}
	if err := os.RemoveAll(appDir); err != nil {
		log.Printf("purge: failed to remove %s: %v", appDir, err)
		return
	}
	log.Printf("purge: removed %s and all stored credentials", appDir)
}
