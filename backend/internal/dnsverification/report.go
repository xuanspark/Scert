package dnsverification

type Stage struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type Report struct {
	RecordName string  `json:"record_name"`
	Stages     []Stage `json:"stages"`
}
