package capture

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strings"
)

// INDIVersion reads the version of indi-bin from an image's
// /etc/indi-versions, which holds one package and its version on each
// line, as dpkg-query -W prints them. The indi images and the image of
// the bare drivers both write it.
func INDIVersion(versions []byte) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(versions))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[0] == "indi-bin" {
			return fields[1], nil
		}
	}
	return "", fmt.Errorf("the versions name no indi-bin: %q", versions)
}

// PinnedINDI reads the indi entry of [package.upstream] from
// indi/package.toml: the version of the indi packages that the images
// install.
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
	return "", fmt.Errorf("%s names no indi in [package.upstream]", packageToml)
}
