package client

import (
    "io"
    "log"
    "bytes"
    "net/http"
    "time"
    "io/ioutil"
    "fmt"
    "compress/gzip"
)

type HttpClient struct {
    client           *http.Client
    config           *HttpConfig
}

type HttpConfig struct {
    URL              string
    URLs             []string
    Headers          map[string]string
    ContentEncoding  string
    Username         string
    Password         string
}

type Response struct {
    Body             []byte
    StatusCode       int
    Header           http.Header
}

func NewHttpClient(config *HttpConfig) *HttpClient {
    client := &HttpClient{ 
        client: &http.Client{
            Transport: &http.Transport{
                MaxIdleConnsPerHost: 10,
                IdleConnTimeout:     90 * time.Second,
                DisableCompression:  false,
            },
            Timeout: 5 * time.Second,
        },
        config: config,
    }
    return client
}

func (h *HttpClient) NewRequest(method, path string, data []byte) (Response, error) {
    var resp Response
    var reader io.ReadCloser

    req, err := http.NewRequest(method, h.config.URL+path, bytes.NewReader(data))
    if err != nil {
        return resp, err
    }

    req.SetBasicAuth(h.config.Username, h.config.Password)
    req.Header.Set("Content-Type", "application/json")

    response, err := h.client.Do(req)
    if err != nil {
        return resp, err
    }
    resp.StatusCode = response.StatusCode
    resp.Header = response.Header
    
    // Check that the server actual sent compressed data
    switch response.Header.Get("Content-Encoding") {
        case "gzip":
            reader, err = gzip.NewReader(response.Body)
            if err != nil {
                return resp, err
            }
            defer reader.Close()
        default:
            reader = response.Body
    }

    body, err := ioutil.ReadAll(reader)
    if err != nil {
        return resp, err
    }
    resp.Body = body

    return resp, nil
}

func (h *HttpClient) WriteRecords(cfg HttpConfig, path string, data []byte) error {
    var buf bytes.Buffer

    if cfg.ContentEncoding == "gzip" {
        writer := gzip.NewWriter(&buf)
        if _, err := writer.Write(data); err != nil {
            return err
        }
        if err := writer.Close(); err != nil {
            return err
        }
    } else {
        buf = *bytes.NewBuffer(data)
    }

    for _, url := range cfg.URLs {

        req, err := http.NewRequest("POST", url+path, &buf)
        if err != nil {
            log.Printf("[error] %s - %v", url, err)
            continue
        }

        req.Header.Set("Content-Type", "application/json")

        if cfg.ContentEncoding == "gzip" {
            req.Header.Set("Content-Encoding", "gzip")
        }

        for name, value := range cfg.Headers {
            req.Header.Set(name, value)
        }

        resp, err := h.client.Do(req)
        if err != nil {
            log.Printf("[error] %s - %v", url, err)
            continue
        }
        io.Copy(ioutil.Discard, resp.Body)
        defer resp.Body.Close()

        if resp.StatusCode >= 400 {
            log.Printf("[error] when writing to [%s] received status code: %d", url+path, resp.StatusCode)
            continue
        }

        return nil
    }

    return fmt.Errorf("failed to complete any request")
}

func (h *HttpClient) ReadRecords(cfg HttpConfig, path string) ([]byte, error) {

    for _, url := range cfg.URLs {

        var reader io.ReadCloser

        req, err := http.NewRequest("GET", url+path, nil)
        if err != nil {
            log.Printf("[error] %s - %v", url, err)
            continue
        }

        for name, value := range cfg.Headers {
            req.Header.Set(name, value)
        }

        req.Header.Set("Accept-Encoding", "gzip")

        r, err := h.client.Do(req)
        if err != nil {
            log.Printf("[error] %s - %v", url, err)
            continue
        }
        defer r.Body.Close()

        // Check that the server actual sent compressed data
        switch r.Header.Get("Content-Encoding") {
            case "gzip":
                reader, err = gzip.NewReader(r.Body)
                if err != nil {
                    log.Printf("[error] %s - %v", url, err)
                    continue
                }
                defer reader.Close()
            default:
                reader = r.Body
        }

        if r.StatusCode >= 400 {
            log.Printf("[error] when reading to [%s] received status code: %d", url+path, r.StatusCode)
            continue
        }

        body, err := ioutil.ReadAll(reader)
        if err != nil {
            log.Printf("[error] when reading to [%s] received error: %v", url+path, err)
            continue
        }

        return body, nil
    }

    return nil, fmt.Errorf("failed to complete any request")
}

func (h *HttpClient) DelRecords(cfg HttpConfig, path string, data []byte) error {
    var buf bytes.Buffer

    if cfg.ContentEncoding == "gzip" {
        writer := gzip.NewWriter(&buf)
        if _, err := writer.Write(data); err != nil {
            return err
        }
        if err := writer.Close(); err != nil {
            return err
        }
    } else {
        buf = *bytes.NewBuffer(data)
    }

    for _, url := range cfg.URLs {

        req, err := http.NewRequest("DELETE", url+path, &buf)
        if err != nil {
            log.Printf("[error] %s - %v", url, err)
            continue
        }

        req.Header.Set("Content-Type", "application/json")

        if cfg.ContentEncoding == "gzip" {
            req.Header.Set("Content-Encoding", "gzip")
        }

        for name, value := range cfg.Headers {
            req.Header.Set(name, value)
        }

        resp, err := h.client.Do(req)
        if err != nil {
            log.Printf("[error] %s - %v", url, err)
            continue
        }
        io.Copy(ioutil.Discard, resp.Body)
        defer resp.Body.Close()

        if resp.StatusCode >= 400 {
            log.Printf("[error] when writing to [%s] received status code: %d", url+path, resp.StatusCode)
            continue
        }

        return nil
    }

    return fmt.Errorf("failed to complete any request")
}