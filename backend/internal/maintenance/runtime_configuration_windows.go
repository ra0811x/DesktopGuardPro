package maintenance

import (
	"errors"
	"fmt"
	"strings"
)

var ErrUnsupportedRuntimeConfiguration = errors.New("custom service names and data directories are not supported by this release")

func validateRuntimeConfiguration(dataDirectory, serviceName string) error {
	expected, err := expectedServiceDataDirectory()
	if err != nil {
		return err
	}
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		serviceName = DefaultServiceName
	}
	if !samePath(dataDirectory, expected) || !strings.EqualFold(serviceName, DefaultServiceName) {
		return fmt.Errorf("%w: use --data-dir %q and --service-name %s", ErrUnsupportedRuntimeConfiguration, expected, DefaultServiceName)
	}
	return nil
}
