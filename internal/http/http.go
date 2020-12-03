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
	URLs                []string           
	Timeout             time.Duration      

	ContentEncoding     string             

	Headers             map[string]string  

	// HTTP Basic Auth Credentials
	Username            string             
	Password            string             

	// Absolute path to file with Bearer token
	BearerToken         string             

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

func (h *HTTP) Gather(method, data string) ([]byte, error) {

	for _, url := range h.URLs {

		request, err := http.NewRequest(method, url, strings.NewReader(data))
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
			log.Printf("[error] %s %s - received status code %d (%s)", method, url, resp.StatusCode, http.StatusText(resp.StatusCode))
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

func (h *HTTP) GatherURL(method, data string) ([]byte, error) {

	body, err := h.Gather(method, data)
	if err != nil {
		return nil, err
	}

	return body, nil
}