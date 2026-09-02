package himama

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

type Activity struct {
	AddedBy  string
	Date     string
	Title    string
	MediaURL string
}

func (a *Activity) SuggestedLocalFilename() (string, error) {
	dateParts := strings.Split(a.Date, "/")
	if len(dateParts) != 3 {
		return "", fmt.Errorf("unexpected date format %q, expected MM/DD/YY", a.Date)
	}
	date := fmt.Sprintf("20%s-%s-%s", dateParts[2], zeroPad(dateParts[0]), zeroPad(dateParts[1]))

	parsedURL, err := url.Parse(a.MediaURL)
	if err != nil {
		return "", fmt.Errorf("unable to parse media URL %q: %w", a.MediaURL, err)
	}

	hash := sha1.New()
	hash.Write([]byte(parsedURL.Path))
	hashStr := hex.EncodeToString(hash.Sum(nil))

	nameParts := []string{
		date,
		sanitizeFilenameComponent(a.AddedBy),
		sanitizeFilenameComponent(a.Title),
		hashStr[0:8],
	}

	ext := strings.ToLower(filepath.Ext(parsedURL.Path))

	return strings.Join(nameParts, " - ") + ext, nil
}

// For stripping unsafe/unwated characters from filename componennts.
// The hyphen is placed last in the class so it is treated as a literal,
// not a range operator (the previous `[^a-zA-Z0-9'-_ ]` accidentally
// allowed '/' and other punctuation via the '-'.."_' range).
var fileNameRegex = regexp.MustCompile(`[^a-zA-Z0-9'_ -]`)

func sanitizeFilenameComponent(part string) string {
	return strings.TrimSpace(fileNameRegex.ReplaceAllString(part, ""))
}

func zeroPad(str string) string {
	if len(str) < 2 {
		return "0" + str
	}
	return str
}
