package plugins

// Remove deletes one installed CLI. It is the step behind
// `kubectl liken plugins remove <domain>`.

import (
	"os"
	"path/filepath"
)

// Remove deletes a domain's CLI from the plugin directory. Removing
// one that is not installed is an error that names the path, so the
// person learns the domain was never there.
func Remove(binDir, domain string) error {
	return os.Remove(filepath.Join(binDir, Name(domain)))
}
