package config

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Listen, Upstream, PublicURL, DataDir string
	Key                                  []byte
	CacheBytes, MaxBookBytes             int64
	BuildConcurrency                     int
}

func FromEnv() (Config, error) {
	c := Config{Listen: env("LISTEN_ADDR", "127.0.0.1:8080"), Upstream: os.Getenv("GRIMMORY_URL"), PublicURL: os.Getenv("PUBLIC_BASE_URL"), DataDir: env("DATA_DIR", "data"), CacheBytes: 2 << 30, MaxBookBytes: 256 << 20, BuildConcurrency: 2}
	var err error
	for _, item := range []struct {
		name   string
		target *int64
	}{{"CACHE_MAX_BYTES", &c.CacheBytes}, {"MAX_EPUB_BYTES", &c.MaxBookBytes}} {
		if s := os.Getenv(item.name); s != "" {
			*item.target, err = strconv.ParseInt(s, 10, 64)
			if err != nil {
				return c, fmt.Errorf("invalid %s", item.name)
			}
		}
	}
	if s := os.Getenv("BUILD_CONCURRENCY"); s != "" {
		c.BuildConcurrency, err = strconv.Atoi(s)
		if err != nil {
			return c, fmt.Errorf("invalid BUILD_CONCURRENCY")
		}
	}
	c.Key, err = base64.StdEncoding.DecodeString(os.Getenv("ADAPTER_ENCRYPTION_KEY"))
	if err != nil || len(c.Key) != 32 {
		return c, fmt.Errorf("ADAPTER_ENCRYPTION_KEY must be a base64-encoded 32-byte key")
	}
	for _, item := range []struct {
		name   string
		target *string
	}{{"GRIMMORY_URL", &c.Upstream}, {"PUBLIC_BASE_URL", &c.PublicURL}} {
		u, e := url.Parse(*item.target)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return c, fmt.Errorf("%s must be an absolute HTTP(S) URL without credentials, query or fragment", item.name)
		}
		if item.name == "PUBLIC_BASE_URL" && strings.Trim(u.Path, "/") != "" {
			return c, fmt.Errorf("PUBLIC_BASE_URL must use a dedicated host without a path prefix")
		}
		*item.target = strings.TrimRight(u.String(), "/")
	}
	if c.MaxBookBytes <= 0 || c.CacheBytes < 2*c.MaxBookBytes || c.BuildConcurrency < 1 || c.BuildConcurrency > 16 {
		return c, fmt.Errorf("cache must hold two maximum EPUBs; sizes must be positive and concurrency 1..16")
	}
	return c, nil
}

func env(name, fallback string) string {
	if s := os.Getenv(name); s != "" {
		return s
	}
	return fallback
}
