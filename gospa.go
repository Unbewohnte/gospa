/*
The MIT License (MIT)

Copyright © 2023 Kasyanov Nikolay Alexeyevich (Unbewohnte)

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the “Software”), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED “AS IS”, WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
*/

package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const VERSION string = "v0.1"

var (
	help    *bool   = flag.Bool("help", false, "Print help message and exit")
	version *bool   = flag.Bool("version", false, "Print version information and exit")
	urlStr  *string = flag.String("url", "", "Specify URL to the webpage to be saved")
)

// matches href="link" or something down bad like hReF =  'link'
var tagHrefRegexp *regexp.Regexp = regexp.MustCompile(`(?i)(href)[\s]*=[\s]*("|')(.*?)("|')`)

// matches src="link" or even something along the lines of SrC    =  'link'
var tagSrcRegexp *regexp.Regexp = regexp.MustCompile(`(?i)(src)[\s]*=[\s]*("|')(.*?)("|')`)

// Fix relative link and construct an absolute one. Does nothing if the URL already looks alright
func resolveLink(link url.URL, fromHost string) *url.URL {
	var resolvedLink url.URL = link

	if !link.IsAbs() {
		if link.Scheme == "" {
			// add scheme
			resolvedLink.Scheme = "https"
		}

		if link.Host == "" {
			// add host
			resolvedLink.Host = fromHost
		}
	}

	return &resolvedLink
}

// Cleans link from form data
func cleanLink(link url.URL, fromHost string) *url.URL {
	resolvedLink := resolveLink(link, fromHost)
	cleanLink, _ := url.Parse(resolvedLink.Scheme + "://" + resolvedLink.Host + resolvedLink.Path)

	return cleanLink
}

// Find all links on page that are specified in <a> tag
func findPageLinks(pageBody []byte) []*url.URL {
	var urls []*url.URL

	for _, match := range tagHrefRegexp.FindAllString(string(pageBody), -1) {
		var linkStartIndex int
		var linkEndIndex int

		linkStartIndex = strings.Index(match, "\"")
		if linkStartIndex == -1 {
			linkStartIndex = strings.Index(match, "'")
			if linkStartIndex == -1 {
				continue
			}

			linkEndIndex = strings.LastIndex(match, "'")
			if linkEndIndex == -1 {
				continue
			}
		} else {
			linkEndIndex = strings.LastIndex(match, "\"")
			if linkEndIndex == -1 {
				continue
			}
		}
		if linkEndIndex <= linkStartIndex+1 {
			continue
		}

		parsedURL, err := url.Parse(match[linkStartIndex+1 : linkEndIndex])
		if err != nil {
			continue
		}

		urls = append(urls, parsedURL)
	}

	return urls
}

func findPageSrcLinks(pageBody []byte) []*url.URL {
	var urls []*url.URL

	// for every element that has "src" attribute
	for _, match := range tagSrcRegexp.FindAllString(string(pageBody), -1) {
		var linkStartIndex int
		var linkEndIndex int

		linkStartIndex = strings.Index(match, "\"")
		if linkStartIndex == -1 {
			linkStartIndex = strings.Index(match, "'")
			if linkStartIndex == -1 {
				continue
			}

			linkEndIndex = strings.LastIndex(match, "'")
			if linkEndIndex == -1 {
				continue
			}
		} else {
			linkEndIndex = strings.LastIndex(match, "\"")
			if linkEndIndex == -1 {
				continue
			}
		}

		if linkEndIndex <= linkStartIndex+1 {
			continue
		}

		parsedURL, err := url.Parse(match[linkStartIndex+1 : linkEndIndex])
		if err != nil {
			continue
		}

		urls = append(urls, parsedURL)
	}

	return urls
}

func findPageFileContentURLs(pageBody []byte) []*url.URL {
	var urls []*url.URL

	for _, link := range findPageLinks(pageBody) {
		if strings.Contains(link.Path, ".css") ||
			strings.Contains(link.Path, ".scss") ||
			strings.Contains(link.Path, ".js") ||
			strings.Contains(link.Path, ".mjs") {
			urls = append(urls, link)
		}
	}
	urls = append(urls, findPageSrcLinks(pageBody)...)

	return urls
}

func savePage(pageBody []byte, saveDirPath string, from *url.URL) error {
	// Create directory with all file content on the page
	var pageFilesDirectoryName string = fmt.Sprintf(
		"%s_%s_files",
		from.Host,
		strings.ReplaceAll(from.EscapedPath(), "/", "_"),
	)
	err := os.MkdirAll(filepath.Join(saveDirPath, pageFilesDirectoryName), os.ModePerm)
	if err != nil {
		return fmt.Errorf("failed to create directory to store file contents in: %s", err)
	}

	srcLinks := findPageFileContentURLs(pageBody)
	wg := sync.WaitGroup{}
	for _, srcLink := range srcLinks {
		wg.Add(1)

		resolvedLink := resolveLink(*srcLink, from.Host)
		func(link *url.URL, saveDirPath string, wg *sync.WaitGroup) error {
			cleanLink := cleanLink(*srcLink, srcLink.Host)

			defer wg.Done()
			response, err := http.Get(link.String())
			if err != nil {
				return fmt.Errorf("failed to receive response from %s: %s", cleanLink.String(), err)
			}
			defer response.Body.Close()

			contents, err := io.ReadAll(response.Body)
			if err != nil {
				return fmt.Errorf("failed to read response from %s: %s", cleanLink.String(), err)
			}

			outputFile, err := os.Create(filepath.Join(saveDirPath, path.Base(cleanLink.String())))
			if err != nil {
				return fmt.Errorf("failed to create output file for %s: %s", cleanLink.String(), err)
			}
			defer outputFile.Close()

			outputFile.Write(contents)

			return nil
		}(resolvedLink, filepath.Join(saveDirPath, pageFilesDirectoryName), &wg)
	}

	// Redirect old URLs to local files
	for _, srcLink := range srcLinks {
		cleanLink := cleanLink(*srcLink, srcLink.Host)
		pageBody = bytes.ReplaceAll(
			pageBody,
			[]byte(srcLink.String()),
			[]byte("./"+filepath.Join(pageFilesDirectoryName, path.Base(cleanLink.String()))),
		)
	}

	// Create page output file
	outfile, err := os.Create(filepath.Join(
		saveDirPath,
		fmt.Sprintf(
			"%s_%s.html",
			from.Host,
			strings.ReplaceAll(from.EscapedPath(), "/", "_")),
	))
	if err != nil {
		fmt.Printf("Failed to create output file: %s\n", err)
		return err
	}
	defer outfile.Close()

	outfile.Write(pageBody)

	wg.Wait()

	return nil
}

func main() {
	flag.Usage = func() {
		fmt.Printf(
			`Gospa - GO and Save this (web) PAge
Usage: gospa (optional)[FLAGs]... (mandatory)-url [webpage URL]

Flags:
-help -> Print this message and exit
-version -> Print version information and exit
-url (string) -> Specify URL to the webpage to be saved
`,
		)
	}
	flag.Parse()

	if *help {
		flag.Usage()
		return
	}

	if *version {
		fmt.Printf("Gospa %s\nBy Kasyanov Nikolay Alexeyevich (Unbewohnte)\n", VERSION)
		return
	}

	*urlStr = strings.TrimSpace(*urlStr)
	if len(*urlStr) == 0 {
		fmt.Printf("URL flag has not been set\n\n")
		flag.Usage()
		return
	}

	parsedURL, err := url.Parse(*urlStr)
	if err != nil {
		fmt.Printf("Invalid URL: %s\n", err)
		return
	}

	workingDir, err := os.Getwd()
	if err != nil {
		fmt.Printf("Failed to figure out working directory: %s\n", err)
		return
	}

	response, err := http.Get(parsedURL.String())
	if err != nil {
		fmt.Printf("Failed to GET %s: %s\n", *urlStr, err)
		return
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		fmt.Printf("Failed to read response from %s: %s\n", *urlStr, err)
		return
	}

	err = savePage(body, workingDir, parsedURL)
	if err != nil {
		fmt.Printf("Failed to save page at %s: %s", parsedURL.String(), err)
		return
	}
}
