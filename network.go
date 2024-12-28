// Copyright 2024 Google Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// This file manages network authentication and retrieval.

package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path/filepath"

	"google.golang.org/api/calendar/v3"

	"golang.org/x/net/context"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
)

// Connect to the Calendar API

// Verify that the client credential permissions are correct before reading them.
func loadClientCredentials(clientSecretPath string) ([]byte, error) {
	// Check if the file exists and is readable
	info, err := os.Stat(clientSecretPath)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("client secret file not found: %s", clientSecretPath)
	}
	// Check if the file has secure permissions (readable only by owner)
	if info.Mode().Perm()&077 != 0 {
		return nil, fmt.Errorf("insecure permissions for client secret file: %s", clientSecretPath)
	}
	// Read the contents of the file
	content, err := ioutil.ReadFile(clientSecretPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read client secret file: %v", err)
	}
	return content, nil
}

func Connect() (*calendar.Service, error) {
	// BEGIN GOOGLE CALENDAR API SAMPLE CODE
	ctx := context.Background()

	b, err := loadClientCredentials(*clientSecretFlag)
	if err != nil {
		log.Fatalf("Unable to read client secret file: %v", err)
	}

	config, err := google.ConfigFromJSON(b, calendar.CalendarReadonlyScope)
	if err != nil {
		log.Fatalf("Unable to parse client secret file to config: %v", err)
	}
	client := getClient(ctx, config)

	srv, err := calendar.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		log.Fatalf("Unable to retrieve Calendar client: %v", err)
	}
	// END GOOGLE CALENDAR API SAMPLE CODE
	return srv, nil
}

// HTTP server code to listen to localhost to get an OAuth2 token.

type handler struct {
	rChan chan string
	srv   *http.Server
}

func (h handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	fmt.Fprintln(debugOut, "Starting HTTP handler")
	url := req.URL
	if url.Path == "/" {
		fmt.Fprintf(w, "Token received.  You can close this window.")
		val := url.Query()
		code := val["code"][0]
		fmt.Fprintf(debugOut, "Received code %v\n", code)
		go h.srv.Shutdown(context.Background())
		h.rChan <- code
	} else {
		w.WriteHeader(http.StatusNotFound)
	}
}

func getTokenFromServer() string {
	rChan := make(chan (string))
	srv := &http.Server{
		Addr: ":8844",
	}
	srv.Handler = handler{
		rChan: rChan,
		srv:   srv,
	}
	srv.ListenAndServe()

	code := <-rChan

	return code
}

// BEGIN GOOGLE CALENDAR API SAMPLE CODE

// getClient uses a Context and Config to retrieve a Token
// then generate a Client. It returns the generated Client.
func getClient(ctx context.Context, config *oauth2.Config) *http.Client {
	cacheFile, err := tokenCacheFile()
	if err != nil {
		log.Fatalf("Unable to get path to cached credential file. %v", err)
	}
	tok, err := tokenFromFile(cacheFile)
	if err != nil {
		tok = getTokenFromWeb(config)
		saveToken(cacheFile, tok)
	}
	return config.Client(ctx, tok)
}

// getTokenFromWeb uses Config to request a Token.
// It returns the retrieved Token.
// Modified from original Google code to use localhost redirect instead of OOB.
func getTokenFromWeb(config *oauth2.Config) *oauth2.Token {
	config.RedirectURL = "http://localhost:8844"
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Go to the following link in your browser: \n%v\n", authURL)

	code := getTokenFromServer()

	tok, err := config.Exchange(oauth2.NoContext, code)
	if err != nil {
		log.Fatalf("Unable to retrieve token from web %v", err)
	}
	return tok
}

// tokenCacheFile generates credential file path/filename.
// It returns the generated credential path/filename.
func tokenCacheFile() (string, error) {
	usr, err := user.Current()
	if err != nil {
		return "", err
	}
	tokenCacheDir := filepath.Join(usr.HomeDir, ".credentials")
	os.MkdirAll(tokenCacheDir, 0700)
	return filepath.Join(tokenCacheDir,
		url.QueryEscape("calendar-blink1.json")), err
}

// tokenFromFile retrieves a Token from a given file path.
// It returns the retrieved Token and any read error encountered.
func tokenFromFile(file string) (*oauth2.Token, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	t := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(t)
	return t, err
}

// saveToken uses a file path to create a file and store the
// token in it.
func saveToken(file string, token *oauth2.Token) {
	fmt.Printf("Saving credential file to: %s\n", file)
	f, err := os.Create(file)
	if err != nil {
		log.Fatalf("Unable to cache oauth token: %v", err)
	}
	defer f.Close()
	json.NewEncoder(f).Encode(token)
}

// END GOOGLE CALENDAR API SAMPLE CODE
