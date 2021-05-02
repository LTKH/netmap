package http

import (
    "log"
    "strings"
    "net/http"
    "time"
    "io/ioutil"
    "fmt"
    "math/rand"
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
        h.Timeout = 5
    }

    h.client = &http.Client{
        Transport: &http.Transport{
            //TLSClientConfig: tlsCfg,
            Proxy:           http.ProxyFromEnvironment,
        },
        Timeout: h.Timeout * time.Second,
    }

    rand.Seed(time.Now().UnixNano())
    rand.Shuffle(len(h.URLs), func(i, j int) { h.URLs[i], h.URLs[j] = h.URLs[j], h.URLs[i] })

    return h
}

func (h *HTTP) Gather(method, path, data string) ([]byte, error) {

    for _, url := range h.URLs {

        request, err := http.NewRequest(method, url+path, strings.NewReader(data))
        if err != nil {
            log.Printf("[error] %s - %v", url, err)
            continue
        }
        
        if method == "POST" {
            request.Header.Set("Content-Type", "application/json")
        }
        
        if h.ContentEncoding == "gzip" {
            request.Header.Set("Content-Encoding", "gzip")
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

        if resp.StatusCode >= 400 {
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

func (h *HTTP) GatherURL(method, path, data string) ([]byte, error) {

    body, err := h.Gather(method, path, data)
    if err != nil {
        return nil, err
    }

    return body, nil
}