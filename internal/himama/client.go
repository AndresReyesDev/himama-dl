package himama

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// HiMama rebranded to Lillio; www.himama.com now 308-redirects here.
const Host = "app.lillio.com"
const BaseURL = "https://" + Host
const LoginURL = BaseURL + "/login"

type Client struct {
	client *http.Client
}

func NewClient(username, password string) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("unable to initialize cookie jar: %w", err)
	}

	client := http.Client{
		Jar: jar,
	}

	if csrfToken, err := getLoginForm(&client); err != nil {
		return nil, err
	} else if err := postLoginForm(&client, csrfToken, username, password); err != nil {
		return nil, err
	}

	return &Client{
		client: &client,
	}, nil
}

func (c *Client) FetchChildren() ([]Child, error) {
	re := regexp.MustCompile(`^/accounts/\d+$`)
	res, err := c.client.Get(BaseURL + "/headlines")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch children: %w", err)
	}
	defer res.Body.Close()

	results, err := filterTags(res.Body, func(node *html.Node) bool {
		if node.Type == html.ElementNode && node.Data == "a" {
			for _, attr := range node.Attr {
				if attr.Key == "href" && re.MatchString(attr.Val) {
					return true
				}
			}
		}
		return false
	})

	if err != nil {
		return nil, fmt.Errorf("failed to fetch children: %w", err)
	}

	children := []Child{}

	for _, result := range results {
		// find the href attribute and extract the account ID
		var id string
		for _, attr := range result.Attr {
			if attr.Key == "href" {
				parts := strings.Split(attr.Val, "/")
				id = parts[len(parts)-1]
				break
			}
		}
		children = append(children, Child{
			Name: strings.TrimSpace(result.FirstChild.Data),
			ID:   id,
		})
	}

	return children, nil
}

func (c *Client) Activities(child Child, page int) ([]Activity, error) {

	results := []Activity{}

	url := fmt.Sprintf("%s/accounts/%s/activities?page=%d", BaseURL, child.ID, page)
	res, err := c.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	rows, err := filterTags(res.Body, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "tr"
	})
	if err != nil {
		return nil, err
	}

	for _, row := range rows {
		if activity, ok := parseActivityRow(row); ok {
			results = append(results, activity)
		}
	}

	return results, nil
}

// parseActivityRow extracts an Activity from a single <tr> of the activities
// table. The layout is rigid — cells sit at fixed odd-numbered child indices
// because whitespace text nodes occupy the even ones — but some rows deviate
// (header rows, or activities whose title cell is shaped differently), so every
// node access is guarded. A row that yields no media URL is reported as not-ok
// and skipped by the caller rather than crashing the whole download.
func parseActivityRow(row *html.Node) (Activity, bool) {
	mediaURL := ""
	if cell := nthChild(row, 17); cell != nil && cell.FirstChild != nil {
		if a := cell.FirstChild.NextSibling; a != nil && a.Data == "a" {
			for _, attr := range a.Attr {
				if attr.Key == "href" {
					mediaURL = attr.Val
					break
				}
			}
		}
	}

	if mediaURL == "" {
		return Activity{}, false
	}

	title := ""
	if cell := nthChild(row, 5); cell != nil && cell.FirstChild != nil {
		if n := cell.FirstChild.NextSibling; n != nil && n.FirstChild != nil {
			title = n.FirstChild.Data
		}
	}

	return Activity{
		AddedBy:  nodeText(nthChild(row, 1)),
		Date:     nodeText(nthChild(row, 3)),
		Title:    title,
		MediaURL: mediaURL,
	}, true
}

// getLoginForm fetches the login form, decorates client (via its cookiejar) with a session cookie,
// and extacts the csrf-token from the response body so it can be used in postLoginForm to submit credentials
func getLoginForm(client *http.Client) (csrfToken string, err error) {
	// First, fetch the login form so we can extract the cookie and authenticity token
	res, err := client.Get(LoginURL)
	if err != nil {
		return "", fmt.Errorf("unable to get %s: %w", LoginURL, err)
	}
	defer res.Body.Close()

	if res.StatusCode != 200 {
		return "", fmt.Errorf("GET %s: Unxpected response %d (want 200)", LoginURL, res.StatusCode)
	}

	token, err := extractCSRFToken(res.Body)
	if err != nil {
		return "", fmt.Errorf("GET %s: %w", LoginURL, err)
	}

	return token, nil
}

// extractCSRFToken pulls the value of <meta name="csrf-token" content="..."> out
// of an HTML document. It uses the HTML parser rather than a regex so it is
// indifferent to whether the markup quotes the attribute values, the order of
// the attributes, or any base64 padding in the token (lillio currently serves
// the tag unquoted; Rails' default helper emits it double-quoted).
func extractCSRFToken(body io.Reader) (string, error) {
	metas, err := filterTags(body, func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.Data == "meta" {
			for _, attr := range n.Attr {
				if attr.Key == "name" && attr.Val == "csrf-token" {
					return true
				}
			}
		}
		return false
	})
	if err != nil {
		return "", err
	}

	for _, meta := range metas {
		for _, attr := range meta.Attr {
			if attr.Key == "content" && attr.Val != "" {
				return attr.Val, nil
			}
		}
	}

	return "", fmt.Errorf("Cannot find authenticity token in response")
}

// postLoginForm submits the login request and decoartes the given client (via its cookie jar) with authentication
func postLoginForm(client *http.Client, csrfToken string, username, password string) error {
	data := url.Values{}
	data.Set("authenticity_token", csrfToken)
	data.Set("utf8", "✓")
	data.Set("user[login]", username)
	data.Set("user[password]", password)
	data.Set("user[remember_me]", "0")
	res, err := client.PostForm(LoginURL, data)
	if err != nil {
		return fmt.Errorf("POST %s Error: %w", LoginURL, err)
	}

	// TODO: Detect login failure

	defer res.Body.Close()

	return nil
}

func filterTags(htmlDoc io.Reader, filter func(*html.Node) bool) ([]*html.Node, error) {
	doc, err := html.Parse(htmlDoc)
	if err != nil {
		return nil, fmt.Errorf("error parsing HTML: %w", err)
	}

	results := []*html.Node{}

	// Recurisively scans the document for any tags for which the given filter function return true
	var f func(*html.Node)
	f = func(n *html.Node) {
		if filter(n) {
			results = append(results, n)
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(doc)

	return results, nil
}

func nodeText(n *html.Node) string {
	if n == nil {
		return ""
	}
	child := n.FirstChild
	if child == nil {
		return ""
	}
	if child.Type == html.TextNode {
		return strings.TrimSpace(child.Data)
	} else {
		return ""
	}
}

func nthChild(n *html.Node, index int) *html.Node {
	c := n.FirstChild
	for i := 0; i < index; i++ {
		c = c.NextSibling
		if c == nil {
			break
		}
	}
	return c
}
