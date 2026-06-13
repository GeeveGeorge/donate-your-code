package record

import "encoding/json"

// ShardPath returns the content-addressed staging path for a record id:
// staging/<id[0:2]>/<id[2:4]>/<id>.json. The path is a function of the validated
// content, which the server independently recomputes and checks.
func ShardPath(recordID string) string {
	if len(recordID) < 4 {
		return "staging/" + recordID + ".json"
	}
	return "staging/" + recordID[0:2] + "/" + recordID[2:4] + "/" + recordID + ".json"
}

// Marshal returns the on-disk JSON for a record (indented for readability). The
// server recomputes record_id from this file's content via the canonicalization
// spec, so the exact formatting here is not security-relevant.
func (r *Record) Marshal() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}
