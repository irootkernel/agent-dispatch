package syncrecords

import (
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var (
	ErrEndpointURL      = errors.New("invalid HTTPS origin URL")
	ErrEndpointData     = errors.New("endpoint contains userinfo, query, or fragment")
	ErrEndpointPort     = errors.New("invalid HTTPS port")
	ErrEndpointPath     = errors.New("endpoint contains a path")
	ErrEndpointHost     = errors.New("endpoint is not a Tailscale .ts.net host")
	endpointPortPattern = regexp.MustCompile(`^[1-9][0-9]*$`)
)

// ParseTailnetEndpoint validates the same origin used by configuration,
// signed membership, schema checks, and outbound peer requests.
func ParseTailnetEndpoint(raw string) (*url.URL, error) {
	if !strings.HasPrefix(raw, "https://") {
		return nil, ErrEndpointURL
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Hostname() == "" || u.Opaque != "" {
		return nil, ErrEndpointURL
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return nil, ErrEndpointData
	}
	if strings.HasSuffix(u.Host, ":") {
		return nil, ErrEndpointPort
	}
	if port := u.Port(); port != "" {
		value, err := strconv.ParseUint(port, 10, 16)
		if !endpointPortPattern.MatchString(port) || err != nil || value == 0 {
			return nil, ErrEndpointPort
		}
	}
	if u.RawPath != "" || (u.Path != "" && u.Path != "/") {
		return nil, ErrEndpointPath
	}
	if !endpointHostPattern.MatchString(u.Hostname()) {
		return nil, ErrEndpointHost
	}
	return u, nil
}
