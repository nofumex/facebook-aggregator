package facebook

import "testing"

func TestImageURLsRejectsLinksAndVideos(t *testing.T) {
	photo := "https://scontent.xx.fbcdn.net/v/t39.30808-6/room.jpg?x=1"
	got := imageURLs([]string{photo, photo, "https://facebook.com/groups/1/posts/2", "https://video.xx.fbcdn.net/v/clip.mp4"})
	if len(got) != 1 || got[0] != photo {
		t.Fatalf("images=%v", got)
	}
}
