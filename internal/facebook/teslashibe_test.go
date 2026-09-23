package facebook

import (
	"net/http"
	"testing"
)

func TestImageURLsRejectsLinksAndVideos(t *testing.T) {
	photo := "https://scontent.xx.fbcdn.net/v/t39.30808-6/room.jpg?x=1"
	got := imageURLs([]string{photo, photo, "https://facebook.com/groups/1/posts/2", "https://video.xx.fbcdn.net/v/clip.mp4"})
	if len(got) != 1 || got[0] != photo {
		t.Fatalf("images=%v", got)
	}
}

func TestFacebookHTTP1TransportIsScoped(t *testing.T) {
	before := http.DefaultTransport.(*http.Transport).ForceAttemptHTTP2
	c := facebookHTTP1Client()
	tr := c.Transport.(*http.Transport)
	if tr.ForceAttemptHTTP2 || len(tr.TLSNextProto) != 0 {
		t.Fatal("HTTP/2 was not disabled")
	}
	if http.DefaultTransport.(*http.Transport).ForceAttemptHTTP2 != before {
		t.Fatal("global transport was mutated")
	}
}
