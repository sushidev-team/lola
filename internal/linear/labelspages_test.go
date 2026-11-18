package linear

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Labels must follow pageInfo: a name lookup over page one only would miss
// labels (and pass a uniqueness check it should fail).
func TestLabelsFollowsPages(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Variables map[string]any }
		_ = json.NewDecoder(r.Body).Decode(&body)
		calls++
		if body.Variables["after"] == nil {
			w.Write([]byte(`{"data":{"team":{"labels":{"nodes":[{"id":"a","name":"one"}],"pageInfo":{"hasNextPage":true,"endCursor":"c1"}}}}}`))
			return
		}
		if body.Variables["after"] != "c1" {
			t.Errorf("after = %v", body.Variables["after"])
		}
		w.Write([]byte(`{"data":{"team":{"labels":{"nodes":[{"id":"b","name":"two"}],"pageInfo":{"hasNextPage":false}}}}}`))
	}))
	defer srv.Close()
	c, _ := fastClient(srv)
	got, err := c.Labels(context.Background(), "t1")
	if err != nil || len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" || calls != 2 {
		t.Fatalf("got %+v err=%v calls=%d", got, err, calls)
	}
}
