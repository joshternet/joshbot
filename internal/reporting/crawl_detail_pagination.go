package reporting

import (
	"context"
	"net/http"
	"strconv"
)

// crawlDetailReader extends legacy readers with bounded database retrieval.
type crawlDetailReader interface {
	CrawlPage(context.Context, int64, int, int64) (CrawlDetail, bool, error)
}

func crawlDetailQuery(request *http.Request, id int64) (int, int64, error) {
	limit, err := requestLimit(request)
	if err != nil {
		return 0, 0, err
	}
	raw := queryValue(request.URL.Query(), "cursor")
	if raw == "" {
		return limit, 0, nil
	}
	cursor, err := decodeCursor(raw, "crawl_pages")
	if err != nil || cursor.Origin != strconv.FormatInt(id, 10) || cursor.At != "" || cursor.ID <= 0 {
		return 0, 0, errInvalidCursor
	}
	return limit, cursor.ID, nil
}

func boundedCrawlDetail(detail CrawlDetail, id int64, limit int, after int64) CrawlDetail {
	pages := detail.Pages
	for len(pages) > 0 && int64(pages[0].Sequence) <= after {
		pages = pages[1:]
	}
	detail.NextCursor = ""
	if len(pages) > limit {
		pages = pages[:limit]
		detail.NextCursor = encodeCursor(cursorPayload{Version: cursorVersion, Kind: "crawl_pages", Origin: strconv.FormatInt(id, 10), ID: int64(pages[len(pages)-1].Sequence)})
	}
	detail.Pages = pages
	return detail
}
