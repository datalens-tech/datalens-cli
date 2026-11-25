package config

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const SchemaVersion = 2

const NoProfilesMessage = "No profiles configured. Run 'datalens setup' or 'datalens profiles create work --installation-type=cloud'."

const (
	defaultName = "default"
	cloudUI     = "https://datalens.ru"
	cloudAPI    = "https://api.datalens.tech"
	teamUI      = "https://datalens.yandex-team.ru"
	teamAPI     = "https://api.datalens.yandex.net"
)

type Profile struct {
	Installation   string   `json:"installationType"`
	Endpoint       string   `json:"endpoint"`
	APIEndpoint    string   `json:"apiEndpoint"`
	OrganizationID string   `json:"organizationId,omitempty"`
	TokenCommand   []string `json:"tokenCommand,omitempty"`
}

type Document struct {
	Version        int                `json:"version"`
	CurrentProfile string             `json:"currentProfile,omitempty"`
	Profiles       map[string]Profile `json:"profiles"`
}

type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func failure(code, message string) error {
	return &Error{Code: code, Message: message}
}

var profileName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func ValidProfileName(name string) bool {
	return profileName.MatchString(name) && name != "." && name != ".."
}

func NormalizeProfile(p Profile) (Profile, error) {
	if p.TokenCommand != nil && (len(p.TokenCommand) == 0 || strings.TrimSpace(p.TokenCommand[0]) == "") {
		return Profile{}, failure("PROFILE_INVALID", "The token command requires an executable.")
	}
	for _, arg := range p.TokenCommand {
		if !utf8.ValidString(arg) || strings.ContainsFunc(arg, unicode.IsControl) {
			return Profile{}, failure("PROFILE_INVALID", "Token command arguments must be valid UTF-8 without control characters.")
		}
	}
	if strings.ContainsFunc(p.OrganizationID, func(r rune) bool { return r < 32 || r == 127 }) {
		return Profile{}, failure("PROFILE_INVALID", "Profile settings must not contain control characters.")
	}
	p.Installation = strings.TrimSpace(p.Installation)
	p.Endpoint = strings.TrimSpace(p.Endpoint)
	p.APIEndpoint = strings.TrimSpace(p.APIEndpoint)
	p.OrganizationID = strings.TrimSpace(p.OrganizationID)

	switch p.Installation {
	case "", "cloud", "yateam", "onpremise-standalone", "stackland":
	default:
		return Profile{}, failure("PROFILE_INVALID", "Installation type must be cloud, yateam, onpremise-standalone, or stackland.")
	}

	if p.Endpoint == "" {
		switch p.Installation {
		case "", "cloud":
			p.Endpoint = cloudUI
		case "yateam":
			p.Endpoint = teamUI
		default:
			return Profile{}, failure("PROFILE_INVALID", "An HTTPS UI endpoint is required for this installation type.")
		}
	}
	ui, err := normalizeEndpoint(p.Endpoint)
	if err != nil {
		return Profile{}, err
	}
	p.Endpoint = ui.String()
	var knownType, api string
	switch ui.Scheme + "://" + ui.Host {
	case cloudUI:
		knownType, api = "cloud", cloudAPI
	case teamUI:
		knownType, api = "yateam", teamAPI
	default:
		api = p.Endpoint + "/api"
	}
	if p.Installation == "" {
		if knownType == "" {
			return Profile{}, failure("PROFILE_INVALID", "Use --installation-type for a custom UI endpoint.")
		}
		p.Installation = knownType
	}
	if knownType != "" && p.Installation != knownType {
		return Profile{}, failure("PROFILE_INVALID", "The installation type does not match the UI endpoint.")
	}
	if p.Installation != "cloud" && p.OrganizationID != "" {
		return Profile{}, failure("PROFILE_INVALID", "Organization IDs are supported only for Cloud profiles.")
	}
	if p.APIEndpoint == "" {
		p.APIEndpoint = api
	} else {
		endpoint, err := normalizeEndpoint(p.APIEndpoint)
		if err != nil {
			return Profile{}, err
		}
		p.APIEndpoint = endpoint.String()
	}
	return p, nil
}

func normalizeEndpoint(endpoint string) (*url.URL, error) {
	if !strings.Contains(endpoint, "://") && !strings.HasPrefix(endpoint, "/") {
		endpoint = "https://" + endpoint
	}
	u, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || strings.ContainsAny(endpoint, "?#") {
		return nil, failure("PROFILE_INVALID", "Endpoints must be HTTPS URLs without credentials, query, or fragment.")
	}
	if strings.HasSuffix(u.Host, ":") {
		return nil, failure("PROFILE_INVALID", "Endpoint has an invalid port.")
	}
	if zone := strings.IndexByte(u.Host, '%'); zone >= 0 && strings.HasPrefix(u.Host, "[") {
		u.Host = strings.ToLower(u.Host[:zone]) + u.Host[zone:]
	} else {
		u.Host = strings.ToLower(u.Host)
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, failure("PROFILE_INVALID", "Endpoint has an invalid port.")
		}
		if n == 443 {
			u.Host = strings.TrimSuffix(u.Host, ":"+port)
		}
	}
	return u, nil
}

func (d Document) ActiveName() string {
	if _, ok := d.Profiles[d.CurrentProfile]; ok {
		return d.CurrentProfile
	}
	var first string
	for name := range d.Profiles {
		if first == "" || name < first {
			first = name
		}
	}
	return first
}

func Resolve(doc Document, explicitName, envName string) (string, Profile, error) {
	if len(doc.Profiles) == 0 {
		return "", Profile{}, failure("NO_PROFILES", NoProfilesMessage)
	}
	name := explicitName
	if name == "" {
		name = envName
	}
	if name == "" {
		name = doc.ActiveName()
	}
	if !ValidProfileName(name) {
		return "", Profile{}, failure("PROFILE_INVALID", "Profile names may contain only letters, digits, dots, underscores, and hyphens.")
	}
	p, ok := doc.Profiles[name]
	if !ok {
		return "", Profile{}, failure("PROFILE_NOT_FOUND", "The selected profile does not exist.")
	}
	p, err := NormalizeProfile(p)
	if err != nil {
		return "", Profile{}, err
	}
	return name, p, nil
}
