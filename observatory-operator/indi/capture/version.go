package capture

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strings"
)

// INDIVersion returns the version of indi-bin from the contents of an
// image's /etc/indi-versions. Each line of that file is a package name
// and its version, in the format of dpkg-query -W.
func INDIVersion(versions []byte) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(versions))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[0] == "indi-bin" {
			return fields[1], nil
		}
	}
	return "", fmt.Errorf("/etc/indi-versions has no indi-bin line: %q", versions)
}

// PinnedINDI returns the indi entry of [package.upstream] in the
// package.toml at the given path. It is the INDI version that the indi
// images install.
func PinnedINDI(packageToml string) (string, error) {
	data, err := os.ReadFile(packageToml)
	if err != nil {
		return "", err
	}
	table := ""
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			table = line
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok && table == "[package.upstream]" && strings.TrimSpace(key) == "indi" {
			return strings.Trim(strings.TrimSpace(value), `"`), nil
		}
	}
	return "", fmt.Errorf("%s has no indi entry in [package.upstream]", packageToml)
}
