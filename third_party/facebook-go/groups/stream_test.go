package groups

import (
	"encoding/json"
	"testing"
)

func TestMergeIncrementalFeed(t *testing.T) {
	var root any
	_ = json.Unmarshal([]byte(`{"group":{"id":"g","group_feed":{"edges":[{"node":{"__typename":"Story","post_id":"1"}}]}}}`), &root)
	var edge any
	_ = json.Unmarshal([]byte(`{"node":{"__typename":"Story","post_id":"2","actors":[{"id":"a","name":"A"}],"comet_sections":{"content":{"story":{"message":{"text":"hello"}}},"timestamp":{"story":{"creation_time":1700000000}}}}}`), &edge)
	root = mergeJSONPath(root, []any{"group", "group_feed", "edges", float64(1)}, edge)
	root = mergeJSONPath(root, []any{"group", "group_feed"}, map[string]any{"page_info": map[string]any{"has_next_page": true, "end_cursor": "cursor"}})
	raw, _ := json.Marshal(root)
	var data singleFeedData
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	page := data.toFeedPage()
	if len(page.Posts) != 2 || page.Posts[1].Message != "hello" || page.Posts[1].AuthorName != "A" || page.Posts[1].CreatedAt.IsZero() || !page.HasNext {
		t.Fatalf("%+v", page)
	}
}

func TestAttachmentCollectsNestedAlbumImages(t *testing.T) {
	var attachment fbAttachment
	raw := []byte(`{"styles":{"attachment":{"subattachments":{"nodes":[{"media":{"image":{"uri":"https://scontent.fbcdn.net/one.jpg"}}},{"media":{"image":{"uri":"https://scontent.fbcdn.net/two.jpg"}}}]}}}}`)
	if err := json.Unmarshal(raw, &attachment); err != nil {
		t.Fatal(err)
	}
	if len(attachment.URLs) != 2 || attachment.URLs[0] != "https://scontent.fbcdn.net/one.jpg" || attachment.URLs[1] != "https://scontent.fbcdn.net/two.jpg" {
		t.Fatalf("urls=%v", attachment.URLs)
	}
}
