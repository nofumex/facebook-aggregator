package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/egori/facebook-aggregator/internal/config"
	fb "github.com/egori/facebook-aggregator/internal/facebook"
	"github.com/joho/godotenv"
	"github.com/teslashibe/facebook-go/groups"
	"os"
	"strings"
	"time"
)

func main() {
	_ = godotenv.Load()
	target := flag.String("group", "253329090046313", "Facebook group ID or URL")
	shape := flag.Bool("shape", false, "print a value-redacted GraphQL shape")
	pages := flag.Int("pages", 1, "maximum feed pages")
	flag.Parse()
	cfg, e := config.Load()
	if e != nil && cfg.Facebook.CUser == "" {
		fmt.Fprintln(os.Stderr, "configuration:", e)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var observed []byte
	a, e := fb.NewTeslaShibe(fb.TeslaShibeConfig{Cookies: groups.Cookies{SB: cfg.Facebook.SB, DATR: cfg.Facebook.DATR, CUser: cfg.Facebook.CUser, XS: cfg.Facebook.XS, FR: cfg.Facebook.FR, PSL: cfg.Facebook.PSL, PSN: cfg.Facebook.PSN}, MinRequestGap: cfg.Facebook.MinRequestGap, MaxRetries: cfg.Facebook.MaxRetries, DocIDs: cfg.Facebook.DocIDs, ResponseObserver: func(b []byte) { observed = b }})
	if e != nil {
		fmt.Fprintln(os.Stderr, "bootstrap failed:", e)
		os.Exit(1)
	}
	id, name, url, e := a.ResolveGroup(ctx, *target)
	if e != nil {
		fmt.Fprintln(os.Stderr, "resolve failed:", e)
		os.Exit(1)
	}
	result, e := a.FetchRecent(ctx, fb.FetchRequest{GroupID: id, MaxPages: *pages})
	if e != nil {
		fmt.Fprintln(os.Stderr, "feed failed:", e)
		if *shape {
			printShape(observed)
		}
		os.Exit(1)
	}
	fmt.Printf("OK group=%q id=%s url=%s posts=%d\n", name, id, url, len(result.Posts))
	for i, p := range result.Posts {
		if i == 3 {
			break
		}
		fmt.Printf("post=%s published=%s text_bytes=%d media=%d\n", p.ID, p.PublishedAt.Format(time.RFC3339), len(p.Text), len(p.MediaURLs))
	}
	if *shape {
		printShape(observed)
	}
}

func printShape(raw []byte) {
	lines := strings.Split(string(raw), "\n")
	fmt.Printf("SHAPE response_bytes=%d lines=%d\n", len(raw), len(lines))
	count := 0
	for lineNo, line := range lines {
		line = strings.TrimPrefix(strings.TrimSpace(line), "for (;;);")
		var v any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			prefix := line
			if len(prefix) > 80 {
				prefix = prefix[:80]
			}
			fmt.Printf("SHAPE line=%d invalid len=%d prefix=%q err=%v\n", lineNo, len(line), prefix, err)
			continue
		}
		if root, ok := v.(map[string]any); ok {
			keys := make([]string, 0, len(root))
			for k := range root {
				keys = append(keys, k)
			}
			fmt.Printf("SHAPE line=%d root_keys=%s\n", lineNo, strings.Join(keys, ","))
			if path, ok := root["path"]; ok {
				fmt.Printf("SHAPE line=%d path=%v\n", lineNo, path)
			}
			if errs, ok := root["errors"].([]any); ok {
				for _, item := range errs {
					if em, ok := item.(map[string]any); ok {
						fmt.Printf("SHAPE graphql_error=%v\n", em["message"])
					}
				}
			}
			if d, ok := root["data"].(map[string]any); ok {
				keys = keys[:0]
				for k := range d {
					keys = append(keys, k)
				}
				fmt.Printf("SHAPE line=%d data_keys=%s\n", lineNo, strings.Join(keys, ","))
			}
		}
		walk(v, "", &count)
		if count > 250 {
			continue
		}
	}
}
func walk(v any, path string, count *int) {
	if *count > 250 {
		return
	}
	switch x := v.(type) {
	case map[string]any:
		for k, value := range x {
			p := path + "/" + k
			lk := strings.ToLower(k)
			if strings.Contains(lk, "message") || strings.Contains(lk, "creation") || strings.Contains(lk, "actor") || strings.Contains(lk, "attach") || strings.Contains(lk, "page_info") || strings.Contains(lk, "has_next") || k == "node" || k == "story" || k == "post_id" {
				if primitive, ok := value.(bool); ok {
					fmt.Printf("SHAPE %s bool=%v\n", p, primitive)
				} else {
					fmt.Printf("SHAPE %s %T\n", p, value)
				}
				*count++
			}
			walk(value, p, count)
		}
	case []any:
		for i, value := range x {
			if i < 3 {
				walk(value, path+"/[]", count)
			}
		}
	}
}
