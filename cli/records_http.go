package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/ubgo/docsync/records"
)

// The HTTP record source (§9.5 adapters) lives here and not in the root
// library because it opens the network; the root never does. The endpoint
// returns a JSON array of flat objects; query args are forwarded as URL
// parameters and applied again locally so a server that ignores them still
// yields the right rows.
const httpRecordsCap = 8 << 20

// ErrHTTPRecords wraps a failed record request.
var ErrHTTPRecords = errors.New("records: http source failed")

func httpRecords(client *http.Client, endpoint string) records.Source {
	return func(args map[string]string) ([]map[string]string, error) {
		u, err := url.Parse(endpoint)
		if err != nil {
			return nil, err
		}
		q := u.Query()
		for k, v := range args {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
		resp, err := client.Get(u.String())
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%w: %s returned %d", ErrHTTPRecords, endpoint, resp.StatusCode)
		}
		var raw []map[string]any
		if err := json.NewDecoder(io.LimitReader(resp.Body, httpRecordsCap)).Decode(&raw); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrHTTPRecords, err)
		}
		rows := make([]map[string]string, 0, len(raw))
		for _, r := range raw {
			row := map[string]string{}
			for k, v := range r {
				row[k] = fmt.Sprint(v)
			}
			rows = append(rows, row)
		}
		return records.Apply(rows, args)
	}
}
