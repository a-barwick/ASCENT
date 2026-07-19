// Package devices owns persistent browser registration and panel-target input
// validation. It does not treat a device identifier as authorization.
package devices

import (
	"errors"
	"net/url"
	"strings"
)

type Class string

const (
	ClassDesktop Class = "desktop"
	ClassTablet  Class = "tablet"
	ClassPhone   Class = "phone"
	ClassDisplay Class = "display"
)

var ErrInvalidDevice = errors.New("device id, name, or class is invalid")
var ErrInvalidPanelRoute = errors.New("panel route must be a local terminal route")

func Validate(id, name string, class Class) error {
	id = strings.TrimSpace(id)
	name = strings.TrimSpace(name)
	if id == "" || len(id) > 128 || name == "" || len(name) > 80 {
		return ErrInvalidDevice
	}
	switch class {
	case ClassDesktop, ClassTablet, ClassPhone, ClassDisplay:
		return nil
	default:
		return ErrInvalidDevice
	}
}

func ValidatePanelRoute(route string) error {
	if route == "" || len(route) > 512 || !strings.HasPrefix(route, "/") || strings.HasPrefix(route, "//") {
		return ErrInvalidPanelRoute
	}
	parsed, err := url.Parse(route)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.User != nil {
		return ErrInvalidPanelRoute
	}
	if parsed.Path != "/" && !strings.HasPrefix(parsed.Path, "/market") &&
		!strings.HasPrefix(parsed.Path, "/facility") && !strings.HasPrefix(parsed.Path, "/terminal") {
		return ErrInvalidPanelRoute
	}
	return nil
}
