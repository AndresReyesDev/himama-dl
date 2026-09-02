package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"regexp"
	"sync"
	"sync/atomic"

	"golang.org/x/term"

	"github.com/meagar/himama-dl/internal/himama"
)

func main() {
	fmt.Println("himama-dl v0.0.3")

	username, password, err := fetchCredentials()
	if err != nil {
		fmt.Println("Error colleting credentials:", err)
		return
	}

	client, err := himama.NewClient(username, password)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	children, err := client.FetchChildren()
	if err != nil {
		fmt.Println("Error initializing HiMama client:", err)
		return
	}

	chosenChildren, err := selectChildren(children)
	if err != nil {
		fmt.Println("Error selecting children for download:", err)
		return
	}

	for _, c := range chosenChildren {
		if err := scrape(client, c); err != nil {
			fmt.Println("Error downloaded data for", c.Name, ":", err)
			return
		}
	}
	fmt.Printf("Total: %d\nDownloaded %d\nAlready Downloaded: %d\n", total, completed, skipped)
}

func fetchCredentials() (username string, password string, err error) {
	flag.StringVar(&username, "username", "", "HiMama username (ie, your email)")
	flag.StringVar(&password, "password", "", "HiMama password")
	flag.Parse()

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

func scrape(client *himama.Client, child himama.Child) error {
	if err := mkdir("./" + child.Name); err != nil {
		return err
	}

	work := scrapeActivities(client, child)

	// blocks until all downloads are finished
	spawnDownloadWorkers(child, work)

	return nil
}

func spawnDownloadWorkers(child himama.Child, work <-chan himama.Activity) {
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

			dest := "./" + child.Name + "/" + filename
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
	if err := os.Mkdir(path, 0700); err != nil {
		if !os.IsExist(err) {
			return fmt.Errorf("unable to create directory ./%s: %w", path, err)
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