package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

const dayDuration = 24 * time.Hour

// RateLimit represents a rate of Count events per Window.
// A zero Count indicates the rate limit is disabled.
type RateLimit struct {
	Count  int
	Window time.Duration
}

// Disabled reports whether the rate limit is disabled.
func (r RateLimit) Disabled() bool {
	return r.Count == 0
}

// String formats the rate limit in canonical <n>/<window> form, or "0" if disabled.
func (r RateLimit) String() string {
	if r.Disabled() {
		return "0"
	}
	return fmt.Sprintf("%d/%s", r.Count, r.Window)
}

// ParseDuration parses a non-negative Go duration string with additional support
// for days using the 'd' unit (for example "30d" or "1d12h").
func ParseDuration(raw string) (time.Duration, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, errors.New("empty duration")
	}
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
		return 0, fmt.Errorf("invalid duration %q: must be non-negative", raw)
	}
	if s == "0" {
		return 0, nil
	}

	var total time.Duration
	i := 0
	for i < len(s) {
		numStart := i
		hasDigits := false
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			hasDigits = true
			i++
		}
		if i < len(s) && s[i] == '.' {
			i++
			for i < len(s) && s[i] >= '0' && s[i] <= '9' {
				hasDigits = true
				i++
			}
		}
		if !hasDigits {
			return 0, fmt.Errorf("invalid duration %q", raw)
		}
		numPart := s[numStart:i]

		unitStart := i
		for i < len(s) && (s[i] < '0' || s[i] > '9') && s[i] != '.' {
			i++
		}
		unitPart := s[unitStart:i]
		if unitPart == "" {
			return 0, fmt.Errorf("invalid duration %q: missing unit", raw)
		}

		var seg time.Duration
		if unitPart == "d" {
			d, err := parseDays(numPart)
			if err != nil {
				return 0, fmt.Errorf("invalid duration %q: %w", raw, err)
			}
			seg = d
		} else {
			d, err := time.ParseDuration(numPart + unitPart)
			if err != nil {
				return 0, fmt.Errorf("invalid duration %q: %w", raw, err)
			}
			if d < 0 {
				return 0, fmt.Errorf("invalid duration %q: must be non-negative", raw)
			}
			seg = d
		}

		if total > math.MaxInt64-seg {
			return 0, fmt.Errorf("invalid duration %q: overflow", raw)
		}
		total += seg
	}
	return total, nil
}

func parseDays(numPart string) (time.Duration, error) {
	if !strings.Contains(numPart, ".") {
		n, err := strconv.ParseInt(numPart, 10, 64)
		if err != nil {
			return 0, err
		}
		if n < 0 {
			return 0, errors.New("must be non-negative")
		}
		if n > math.MaxInt64/int64(dayDuration) {
			return 0, errors.New("overflow")
		}
		return time.Duration(n) * dayDuration, nil
	}

	f, err := strconv.ParseFloat(numPart, 64)
	if err != nil {
		return 0, err
	}
	if f < 0 || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, errors.New("must be finite and non-negative")
	}
	ns := f * float64(dayDuration)
	if ns > float64(math.MaxInt64) {
		return 0, errors.New("overflow")
	}
	return time.Duration(ns), nil
}

// ParseSize parses a non-negative byte size supporting integer bytes and
// SI (KB, MB, GB) or IEC (KiB, MiB, GiB) suffixes, plus optional B.
func ParseSize(raw string) (int64, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, errors.New("empty size")
	}
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
		return 0, fmt.Errorf("invalid size %q: must be non-negative", raw)
	}

	split := 0
	for split < len(s) && s[split] >= '0' && s[split] <= '9' {
		split++
	}
	if split == 0 {
		return 0, fmt.Errorf("invalid size %q", raw)
	}

	numStr := s[:split]
	unit := s[split:]

	var mult int64
	switch unit {
	case "", "B":
		mult = 1
	case "KB":
		mult = 1000
	case "MB":
		mult = 1000 * 1000
	case "GB":
		mult = 1000 * 1000 * 1000
	case "KiB":
		mult = 1024
	case "MiB":
		mult = 1024 * 1024
	case "GiB":
		mult = 1024 * 1024 * 1024
	default:
		return 0, fmt.Errorf("invalid size %q: unknown unit %q", raw, unit)
	}

	n, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: %w", raw, err)
	}
	if n > math.MaxInt64/mult {
		return 0, fmt.Errorf("invalid size %q: overflow", raw)
	}
	return n * mult, nil
}

// ParseRate parses a rate limit in the form <n>/<window>, where window is
// "s", "m", "h", "d", or a duration string (e.g. "600/m", "10/15m").
// When allowZero is true, "0" is accepted and returns a disabled RateLimit.
func ParseRate(raw string, allowZero bool) (RateLimit, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return RateLimit{}, errors.New("empty rate")
	}
	if s == "0" {
		if allowZero {
			return RateLimit{}, nil
		}
		return RateLimit{}, fmt.Errorf("invalid rate %q: zero is not allowed", raw)
	}

	countStr, winStr, ok := strings.Cut(s, "/")
	if !ok || countStr == "" || winStr == "" {
		return RateLimit{}, fmt.Errorf("invalid rate %q: expected <count>/<window>", raw)
	}

	count, err := strconv.Atoi(countStr)
	if err != nil || count <= 0 {
		return RateLimit{}, fmt.Errorf("invalid rate %q: count must be a positive integer", raw)
	}

	var window time.Duration
	switch winStr {
	case "s":
		window = time.Second
	case "m":
		window = time.Minute
	case "h":
		window = time.Hour
	case "d":
		window = dayDuration
	default:
		window, err = ParseDuration(winStr)
		if err != nil {
			return RateLimit{}, fmt.Errorf("invalid rate %q: %w", raw, err)
		}
	}
	if window <= 0 {
		return RateLimit{}, fmt.Errorf("invalid rate %q: window must be positive", raw)
	}

	return RateLimit{Count: count, Window: window}, nil
}

// ParseBool parses a boolean string strictly accepting true, false, 1, or 0.
func ParseBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1":
		return true, nil
	case "false", "0":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean %q: expected true, false, 1, or 0", raw)
	}
}

// ParseTrustedProxies parses a comma-separated list of CIDR prefixes.
func ParseTrustedProxies(raw string) ([]netip.Prefix, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	out := make([]netip.Prefix, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, fmt.Errorf("invalid CIDR list %q: empty entry", raw)
		}
		prefix, err := netip.ParsePrefix(p)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", p, err)
		}
		out = append(out, prefix.Masked())
	}
	return out, nil
}

var reservedSlugs = map[string]struct{}{
	"api":      {},
	"admin":    {},
	"assets":   {},
	"render":   {},
	"setup":    {},
	"login":    {},
	"logout":   {},
	"invite":   {},
	"reset":    {},
	"settings": {},
	"events":   {},
	"healthz":  {},
	"readyz":   {},
	"static":   {},
	"new":      {},
}

// ValidateSlug checks whether s satisfies the project slug rules (SPEC 7.3).
func ValidateSlug(s string) error {
	if len(s) < 1 || len(s) > 63 {
		return fmt.Errorf("slug %q must be between 1 and 63 characters", s)
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		isLower := c >= 'a' && c <= 'z'
		isDigit := c >= '0' && c <= '9'
		if !isLower && !isDigit && c != '-' {
			return fmt.Errorf("slug %q contains invalid character %q", s, c)
		}
	}
	if s[0] == '-' || s[len(s)-1] == '-' {
		return fmt.Errorf("slug %q must start and end with a lowercase letter or digit", s)
	}
	if len(s) >= 4 && s[2] == '-' && s[3] == '-' {
		return fmt.Errorf("slug %q cannot contain '--' at positions 3 and 4", s)
	}
	if _, reserved := reservedSlugs[s]; reserved {
		return fmt.Errorf("slug %q is reserved", s)
	}
	return nil
}

// latinFold maps common Latin diacritic runes to ASCII equivalents (SPEC 7.3).
var latinFold = map[rune]string{
	'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a", 'ā': "a", 'ă': "a", 'ą': "a",
	'æ': "ae",
	'ç': "c", 'ć': "c", 'ĉ': "c", 'ċ': "c", 'č': "c",
	'ď': "d", 'đ': "d", 'ð': "d",
	'è': "e", 'é': "e", 'ê': "e", 'ë': "e", 'ē': "e", 'ĕ': "e", 'ė': "e", 'ę': "e", 'ě': "e",
	'ĝ': "g", 'ğ': "g", 'ġ': "g", 'ģ': "g",
	'ĥ': "h", 'ħ': "h",
	'ì': "i", 'í': "i", 'î': "i", 'ï': "i", 'ĩ': "i", 'ī': "i", 'ĭ': "i", 'į': "i", 'ı': "i",
	'ĵ': "j",
	'ķ': "k",
	'ĺ': "l", 'ļ': "l", 'ľ': "l", 'ŀ': "l", 'ł': "l",
	'ñ': "n", 'ń': "n", 'ņ': "n", 'ň': "n",
	'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o", 'ö': "o", 'ø': "o", 'ō': "o", 'ŏ': "o", 'ő': "o",
	'œ': "oe",
	'ŕ': "r", 'ŗ': "r", 'ř': "r",
	'ś': "s", 'ŝ': "s", 'ş': "s", 'š': "s", 'ß': "ss",
	'ţ': "t", 'ť': "t", 'ŧ': "t", 'þ': "th",
	'ù': "u", 'ú': "u", 'û': "u", 'ü': "u", 'ũ': "u", 'ū': "u", 'ŭ': "u", 'ů': "u", 'ű': "u", 'ų': "u",
	'ŵ': "w",
	'ý': "y", 'ÿ': "y", 'ŷ': "y",
	'ź': "z", 'ż': "z", 'ž': "z",
}

// Slugify converts an arbitrary name or SMTP username into a valid project slug
// following SPEC 7.3: lowercase, transliterate Latin diacritics, replace invalid
// characters with '-', collapse repeats, trim '-', truncate to 63 bytes, fall
// back to fallback when empty, and append "-1" for reserved slugs.
func Slugify(raw, fallback string) string {
	if fallback == "" {
		fallback = defaultProject
	}

	var b strings.Builder
	b.Grow(len(raw))
	lastDash := true

	writeRune := func(r rune) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}

	for _, r := range strings.ToLower(raw) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			writeRune(r)
			continue
		}
		if repl, ok := latinFold[r]; ok {
			for _, cr := range repl {
				writeRune(cr)
			}
			continue
		}
		writeRune('-')
	}

	slug := strings.Trim(b.String(), "-")
	if len(slug) > 63 {
		slug = strings.TrimRight(slug[:63], "-")
	}
	if slug == "" {
		return fallback
	}
	if _, reserved := reservedSlugs[slug]; reserved {
		return slug + "-1"
	}
	return slug
}

func validateDomain(d string) error {
	if len(d) == 0 || len(d) > 253 {
		return fmt.Errorf("domain %q must be between 1 and 253 characters", d)
	}
	if strings.HasPrefix(d, ".") || strings.HasSuffix(d, ".") {
		return fmt.Errorf("domain %q must not start or end with a dot", d)
	}
	for _, label := range strings.Split(d, ".") {
		if len(label) < 1 || len(label) > 63 {
			return fmt.Errorf("domain %q has invalid label %q", d, label)
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("domain %q has invalid label %q", d, label)
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return fmt.Errorf("domain %q contains invalid character %q", d, c)
			}
		}
	}
	return nil
}

func parseBase64Key32(raw string) ([]byte, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, errors.New("empty key (generate one with: mailcubby keygen)")
	}
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, enc := range encodings {
		b, err := enc.DecodeString(s)
		if err == nil {
			if len(b) != 32 {
				return nil, fmt.Errorf("must decode to 32 bytes, got %d (generate one with: mailcubby keygen)", len(b))
			}
			return b, nil
		}
	}
	return nil, errors.New("must be valid base64 (standard or URL) encoding of 32 bytes (generate one with: mailcubby keygen)")
}
