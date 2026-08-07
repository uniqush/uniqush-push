package http_api //nolint:revive

// APNSErrorResponse is struct to represent JSON data returned by APNs HTTP API
// if push request is not successful
type APNSErrorResponse struct {
	Reason string
	// Unix timestamp. might be in milliseconds. Not used yet.
	Timestamp int64
}
