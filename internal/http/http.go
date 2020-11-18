package http

import (
    "log"
	"strings"
	"net/http"
	"time"
	"io/ioutil"
	"fmt"
)

type HTTP struct {
	URLs                []string           `toml:"urls"`
	Timeout             time.Duration      `toml:"timeout"`

	ContentEncoding     string             `toml:"content_encoding"`

	Headers             map[string]string  `toml:"headers"`

	// HTTP Basic Auth Credentials
	Username            string             `toml:"username"`
	Password            string             `toml:"password"`

	// Absolute path to file with Bearer token
	BearerToken         string             `toml:"bearer_token"`

	client              *http.Client
}

func New(h HTTP) HTTP {

	// Set default timeout
	if h.Timeout == 0 {
		h.Timeout = 5000
	}

	h.client = &http.Client{
		Transport: &http.Transport{
			//TLSClientConfig: tlsCfg,
			Proxy:           http.ProxyFromEnvironment,
		},
		//Timeout: h.Timeout,
	}

	return h
}

func (h *HTTP) Gather() ([]byte, error) {

	for _, url := range h.URLs {

		request, err := http.NewRequest("GET", url, nil)
		if err != nil {
			log.Printf("[error] %s - %v", url, err)
			continue
		}

		if h.BearerToken != "" {
			token, err := ioutil.ReadFile(h.BearerToken)
			if err != nil {
				log.Printf("[error] %s - %v", url, err)
				continue
			}
			bearer := "Bearer " + strings.Trim(string(token), "\n")
			request.Header.Set("Authorization", bearer)
		}

		if h.ContentEncoding == "gzip" {
			request.Header.Set("Content-Encoding", "gzip")
		}

		for k, v := range h.Headers {
			if strings.ToLower(k) == "host" {
				request.Host = v
			} else {
				request.Header.Add(k, v)
			}
		}

		if h.Username != "" || h.Password != "" {
			request.SetBasicAuth(h.Username, h.Password)
		}

		resp, err := h.client.Do(request)
		if err != nil {
			log.Printf("[error] %s - %v", url, err)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			log.Printf("[error] %s - received status code %d (%s), expected any value out of 200", url, resp.StatusCode, http.StatusText(resp.StatusCode))
			continue
		}

		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			continue
		}

		return body, nil
	}

	return nil, fmt.Errorf("error failed to complete any request")
}

func (h *HTTP) GatherURL() ([]byte, error) {

	body, err := h.Gather()
	if err != nil {
		return body, err
	}

	return body, nil
}