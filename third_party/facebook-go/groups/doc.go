// Package groups provides a Go client for the Facebook Groups surface.
//
// It supports searching, discovering, joining, posting, commenting,
// and trend-scraping across Facebook Groups using cookie-based auth.
//
// Authentication is cookie-based. Pass the six session cookies obtained from a
// logged-in browser session to [New]. The client performs a one-time session
// bootstrap on construction to extract the CSRF tokens (fb_dtsg, lsd) required
// by every GraphQL request.
//
// Example:
//
//	c, err := groups.New(groups.Cookies{
//	    XS:    "39%3AW11l...",
//	    CUser: "1226944",
//	    SB:    "YXzYZl6g...",
//	    DATR:  "TCeNaTXI...",
//	    FR:    "1pRdJlAZ...",
//	    PSL:   "1",
//	})
//	results, err := c.SearchGroups(ctx, "golang developers")
package groups
