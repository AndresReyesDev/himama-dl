package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/net/html"
	"golang.org/x/term"

	"github.com/AndresReyesDev/himama-dl/internal/himama"
)

func main() {
	fmt.Println("himama-dl v0.0.4")

	username, password, err := fetchCredentials()
	if err != nil {
		fmt.Println("Error collecting credentials:", err)
		return
	}

	client, err := himama.NewClient(username, password)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	children, err := client.FetchChildren()
	if err != nil {
		fmt.Println("Error initializing Lillio client:", err)
		return
	}

	chosenChildren, err := selectChildren(children)
	if err != nil {
		fmt.Println("Error selecting children for download:", err)
		return
	}

	for _, c := range chosenChildren {
		vprintf("Scraping %s (%s)...\n", c.Name, c.ID)
		if err := scrape(client, c); err != nil {
			fmt.Println("Error downloaded data for", c.Name, ":", err)
			return
		}
	}
	fmt.Printf("Total: %d\nDownloaded %d\nAlready Downloaded: %d\n", total, completed, skipped)
	if verbose {
		fmt.Printf("Reports downloaded: %d\nReports skipped: %d\n", reportCompleted, reportSkipped)
	}
}

func fetchCredentials() (username string, password string, err error) {
	flag.StringVar(&username, "username", "", "Lillio username (ie, your email)")
	flag.StringVar(&password, "password", "", "Lillio password")
	flag.StringVar(&outputDir, "output", "output", "Output directory")
	flag.BoolVar(&verbose, "v", false, "Verbose output")
	flag.Parse()

	// Load .env file if it exists
	envVars := loadDotEnv()

	if username == "" {
		username = envVars["HIMAMA_USERNAME"]
	}
	if password == "" {
		password = envVars["HIMAMA_PASSWORD"]
	}

	if username == "" {
		fmt.Print("Username: ")
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		username = scanner.Text()
	}

	if password == "" {
		fmt.Print("Password: ")
		bytePassword, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return "", "", fmt.Errorf("unable to read password: %w", err)
		}
		password = string(bytePassword)
	}

	return
}

var total, completed, skipped int32
var reportCompleted, reportSkipped int32
var outputDir string
var verbose bool

func vprintf(format string, args ...interface{}) {
	if verbose {
		fmt.Printf(format, args...)
	}
}

func loadDotEnv() map[string]string {
	result := map[string]string{}
	f, err := os.Open(".env")
	if err != nil {
		return result
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		val = strings.Trim(val, `"`)
		result[key] = val
	}
	return result
}

func scrape(client *himama.Client, child himama.Child) error {
	childDirName := fmt.Sprintf("%s (%s)", child.Name, child.ID)
	baseDir := filepath.Join(outputDir, childDirName)
	activitiesDir := filepath.Join(baseDir, "Activities")
	reportsDir := filepath.Join(baseDir, "Reports")

	for _, dir := range []string{baseDir, activitiesDir, reportsDir} {
		if err := mkdir(dir); err != nil {
			return err
		}
	}

	work := scrapeActivities(client, child)
	spawnDownloadWorkers(child, activitiesDir, work)

	reportWork := scrapeReports(client, child)
	spawnReportWorkers(client, activitiesDir, reportsDir, reportWork)

	return nil
}

func spawnDownloadWorkers(child himama.Child, activitiesDir string, work <-chan himama.Activity) {
	wg := sync.WaitGroup{}
	// These workers hit S3, so we can parallelize pretty heavily
	tickets := make(chan struct{}, 10)

	for activity := range work {
		tickets <- struct{}{}
		wg.Add(1)
		go func(activity himama.Activity) {
			defer wg.Done()
			filename, err := activity.SuggestedLocalFilename()
			if err != nil {
				fmt.Printf("Skipping activity: %v\n", err)
				<-tickets
				return
			}
			filename = filterWindowsFilename(filename)

			dateDir, err := activity.DateISO()
			if err != nil {
				fmt.Printf("Skipping activity: %v\n", err)
				<-tickets
				return
			}
			dateDir = filterWindowsFilename(dateDir)

			destDir := filepath.Join(activitiesDir, dateDir)
			if err := mkdir(destDir); err != nil {
				fmt.Printf("Error creating directory %s: %v\n", destDir, err)
				<-tickets
				return
			}

			dest := filepath.Join(destDir, filename)
			vprintf("  Activity: %s -> %s\n", activity.MediaURL, dest)
			exists, err := fileExists(dest)
			if err != nil {
				fmt.Printf("Error checking %s: %v\n", dest, err)
				<-tickets
				return
			}
			if !exists {
				if err := download(activity.MediaURL, dest); err != nil {
					fmt.Printf("Error downloading %s: %v\n", dest, err)
					<-tickets
					return
				}
				atomic.AddInt32(&completed, 1)
			} else {
				vprintf("  Already exists, skipping: %s\n", dest)
				atomic.AddInt32(&skipped, 1)
			}

			fmt.Printf("%d/%d: %s\n", completed, total, filename)
			<-tickets
		}(activity)
	}

	wg.Wait()
}

func download(srcURL, destPath string) error {
	res, err := http.Get(srcURL)
	if err != nil {
		return fmt.Errorf("unable to download %s: %w", srcURL, err)
	}
	defer res.Body.Close()
	fh, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("unable to create %s: %w", destPath, err)
	}
	defer fh.Close()
	if _, err := io.Copy(fh, res.Body); err != nil {
		return fmt.Errorf("error writing %s: %w", destPath, err)
	}
	return nil
}

// Scraps the activity index pages
// Returns a channel over which activity media links (ie links to S3 objects) are sent
func scrapeReports(client *himama.Client, child himama.Child) <-chan himama.Report {
	work := make(chan himama.Report, 2000)

	wg := sync.WaitGroup{}
	tickets := make(chan struct{}, 5)

	go func() {
		done := false
		for page := 1; !done; page++ {
			wg.Add(1)
			tickets <- struct{}{}
			page := page
			go func() {
				defer wg.Done()

				reports, err := client.Reports(child, page)
				if err != nil {
					fmt.Printf("Error fetching reports page %d: %v\n", page, err)
					done = true
				} else if len(reports) == 0 {
					done = true
				} else {
					vprintf("  Reports page %d: found %d reports\n", page, len(reports))
					for _, report := range reports {
						work <- report
					}
				}
				<-tickets
			}()
		}

		wg.Wait()
		close(work)
	}()

	return work
}

func spawnReportWorkers(client *himama.Client, activitiesDir, reportsDir string, work <-chan himama.Report) {
	wg := sync.WaitGroup{}
	tickets := make(chan struct{}, 5)

	for report := range work {
		tickets <- struct{}{}
		wg.Add(1)
		go func(report himama.Report) {
			defer wg.Done()

			vprintf("  Report raw date: %q, URL: %s\n", report.Date, report.URL)
			dateISO, err := report.DateISO()
			if err != nil {
				fmt.Printf("Skipping report: %v\n", err)
				<-tickets
				return
			}

			dest := filepath.Join(reportsDir, filterWindowsFilename(dateISO)+".html")
			vprintf("  Report: %s -> %s\n", report.URL, dest)
			exists, err := fileExists(dest)
			if err != nil {
				fmt.Printf("Error checking %s: %v\n", dest, err)
				<-tickets
				return
			}
			if !exists {
				dateDir := filepath.Join(activitiesDir, filterWindowsFilename(dateISO))
				if err := downloadReport(client, report.URL, dest, dateDir); err != nil {
					fmt.Printf("Error downloading %s: %v\n", dest, err)
					<-tickets
					return
				}
				fmt.Printf("Report: %s\n", dateISO)
				atomic.AddInt32(&reportCompleted, 1)
			} else {
				vprintf("  Report already exists, skipping: %s\n", dest)
				fmt.Printf("Report (skipped): %s\n", dateISO)
				atomic.AddInt32(&reportSkipped, 1)
			}
			<-tickets
		}(report)
	}

	wg.Wait()
}

func downloadReport(client *himama.Client, srcURL, destPath, activitiesDateDir string) error {
	res, err := client.Get(srcURL)
	if err != nil {
		return fmt.Errorf("unable to fetch %s: %w", srcURL, err)
	}
	defer res.Body.Close()

	doc, err := html.Parse(res.Body)
	if err != nil {
		return fmt.Errorf("unable to parse report HTML: %w", err)
	}

	rewriteMediaURLs(doc, activitiesDateDir)

	content := extractReportContent(doc)

	fh, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("unable to create %s: %w", destPath, err)
	}
	defer fh.Close()

	wrapped := `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>` + filepath.Base(destPath) + `</title>
<style>
body { font-family: 'Helvetica Neue', Arial, sans-serif; max-width: 900px; margin: 20px auto; padding: 0 10px; color: #333; }
h1 { color: #2c3e50; }
h2 { color: #27ae60; margin-top: 20px; }
img, video { max-width: 100%; height: auto; border-radius: 8px; margin: 10px 0; }
.entry-div { margin-bottom: 20px; padding: 10px; background: #f9f9f9; border-radius: 8px; }
hr { border: none; border-top: 1px solid #ddd; margin: 20px 0; }
</style>
</head>
<body>
` + content + `
</body>
</html>`

	if _, err := fh.WriteString(wrapped); err != nil {
		return fmt.Errorf("error writing %s: %w", destPath, err)
	}
	return nil
}

func scrapeActivities(client *himama.Client, child himama.Child) <-chan himama.Activity {
	work := make(chan himama.Activity, 2000)

	wg := sync.WaitGroup{}
	tickets := make(chan struct{}, 5)

	go func() {
		done := false
		for page := 1; !done; page++ {
			wg.Add(1)
			tickets <- struct{}{}
			page := page
			go func() {
				defer wg.Done()

				activities, err := client.Activities(child, page)
				if err != nil {
					fmt.Printf("Error fetching page %d: %v\n", page, err)
					done = true
				} else if len(activities) == 0 {
					done = true
				} else {
					atomic.AddInt32(&total, int32(len(activities)))
					for _, activity := range activities {
						work <- activity
					}
				}
				<-tickets
			}()
		}

		wg.Wait()
		close(work)
	}()

	return work
}

func selectChildren(children []himama.Child) ([]himama.Child, error) {
	if len(children) == 0 {
		fmt.Println("Unable to find children")
		return nil, fmt.Errorf("no children found")
	}

	if len(children) == 1 {
		fmt.Printf("Found 1 child: %s (%s)\n", children[0].Name, children[0].ID)
		return children, nil
	}

	var choice int
	for {
		fmt.Println("Found multiple children. Which account to scrape?")
		for idx, child := range children {
			fmt.Printf("%d. %s (%s)\n", idx+1, child.Name, child.ID)
		}
		fmt.Printf("%d. All\n", len(children)+1)
		fmt.Scanf("%d", &choice)
		if choice >= 1 && choice <= len(children)+1 {
			break
		}
	}

	if choice == len(children)+1 {
		return children, nil
	}

	return []himama.Child{children[choice-1]}, nil
}

func mkdir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		if !os.IsExist(err) {
			return fmt.Errorf("unable to create directory %s: %w", path, err)
		}
	}
	return nil
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)

	if err == nil {
		return true, nil
	} else if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}

	return false, err
}

func filterWindowsFilename(input string) string {
	// Define a regular expression to match characters that are not allowed in Windows filenames
	regex := regexp.MustCompile(`[<>:"/\\|?*\x00-\x1F]`)

	// Replace disallowed characters with an underscore
	result := regex.ReplaceAllString(input, "_")

	return result
}

// rewriteMediaURLs walks the parsed HTML tree and rewrites <img src>,
// <video src>, and <video poster> attributes that point to S3 media
// URLs. For each URL it computes sha1(path)[:8] — the same hash used in
// activity filenames — and searches activitiesDateDir for a file whose
// name contains that hash. If found, the attribute is rewritten to a
// relative path pointing at the local file.
func rewriteMediaURLs(doc *html.Node, activitiesDateDir string) {
	mediaAttrs := []string{"src", "poster"}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "img" || n.Data == "video") {
			for i, attr := range n.Attr {
				if !contains(mediaAttrs, attr.Key) {
					continue
				}
				localPath := findLocalMedia(attr.Val, activitiesDateDir)
				if localPath != "" {
					n.Attr[i].Val = localPath
					vprintf("  Rewrote media URL: %s -> %s\n", attr.Val, localPath)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// findLocalMedia computes the same hash used in activity filenames
// (sha1 of the URL path, first 8 hex chars) and searches dir for a
// file containing that hash. Returns a relative path like
// "../Activities/2026-09-01/filename.ext" or "" if no match.
func findLocalMedia(rawURL, dir string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}

	h := sha1.New()
	h.Write([]byte(parsed.Path))
	hashStr := hex.EncodeToString(h.Sum(nil))[:8]

	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.Contains(entry.Name(), hashStr) {
			rel, err := filepath.Rel(filepath.Dir(dir), filepath.Join(dir, entry.Name()))
			if err != nil {
				return ""
			}
			return rel
		}
	}
	return ""
}

// extractReportContent finds the report heading and content div in the
// parsed HTML and returns them as an HTML string. It looks for the
// <h1> containing "Report" and the following <div class="row
// margin-bottom-30"> that holds meals, mood, and activities.
func extractReportContent(doc *html.Node) string {
	var h1Text, h5Text string
	var contentDiv *html.Node

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if n.Data == "h1" || n.Data == "h2" {
				text := allNodeText(n)
				if strings.Contains(text, "Report") {
					h1Text = text
				}
			}
			if n.Data == "h5" && h5Text == "" {
				h5Text = allNodeText(n)
			}
			if n.Data == "div" {
				if hasClass(n, "row") && hasClass(n, "margin-bottom-30") && contentDiv == nil {
					contentDiv = n
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	var sb strings.Builder
	if h1Text != "" {
		sb.WriteString("<h1>" + h1Text + "</h1>\n")
	}
	if h5Text != "" {
		sb.WriteString("<h5>" + h5Text + "</h5>\n<hr/>\n")
	}
	if contentDiv != nil {
		renderNode(&sb, contentDiv)
	}
	return sb.String()
}

func allNodeText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sb.WriteString(allNodeText(c))
	}
	return sb.String()
}

func hasClass(n *html.Node, class string) bool {
	for _, attr := range n.Attr {
		if attr.Key == "class" {
			for _, c := range strings.Fields(attr.Val) {
				if c == class {
					return true
				}
			}
		}
	}
	return false
}

// renderNode recursively renders an html.Node and its children back to
// an HTML string.
func renderNode(sb *strings.Builder, n *html.Node) {
	if n.Type == html.TextNode {
		sb.WriteString(n.Data)
		return
	}
	if n.Type != html.ElementNode {
		return
	}
	sb.WriteString("<" + n.Data)
	for _, attr := range n.Attr {
		sb.WriteString(" " + attr.Key + "=\"" + attr.Val + "\"")
	}
	sb.WriteString(">")
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		renderNode(sb, c)
	}
	sb.WriteString("</" + n.Data + ">")
}