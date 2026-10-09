package model

// UploadProxyRequest describes the client request, not the provider request.
type UploadProxyRequest struct {
	Path          string `json:"path"`
	FileName      string `json:"file_name"`
	FileSize      int64  `json:"file_size"`
	Method        string `json:"method"`
	Format        string `json:"format"`
	ContentType   string `json:"content_type"`
	ContentMD5    string `json:"content_md5,omitempty"`
	ContentSHA256 string `json:"content_sha256,omitempty"`
	Overwrite     bool   `json:"overwrite"`
	Response      string `json:"response"`
	WorkerAddress string `json:"worker_address"`
	Sign          string `json:"sign"`
	Hash          string `json:"hash,omitempty"`
	StorageID     uint   `json:"storage_id"`
	StorageTime   int64  `json:"storage_time"`
}

type UploadProxyPlan struct {
	URL       string            `json:"url,omitempty"`
	Method    string            `json:"method,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Encoding  string            `json:"encoding,omitempty"`
	Fields    map[string]string `json:"fields,omitempty"`
	FileField string            `json:"file_field,omitempty"`
	Instant   bool              `json:"instant"`
	State     any               `json:"-"`
}

type UploadProxyResult struct {
	Success bool   `json:"success"`
	Status  int    `json:"status"`
	Body    string `json:"body"`
	Bytes   int64  `json:"bytes"`
	ETag    string `json:"etag"`
}
