package nginxactivation

// State 不包含文件路径、私钥或完整配置，可通过受保护的本地协议返回。
type State struct {
	ID            string `json:"id,omitempty"`
	Status        string `json:"status"`
	PreviousHash  string `json:"previous_hash,omitempty"`
	CandidateHash string `json:"candidate_hash,omitempty"`
}

type ReadyState struct {
	Hostname        string `json:"hostname"`
	ProxyAddress    string `json:"proxy_address"`
	UpstreamAddress string `json:"upstream_address"`
	LeafHash        string `json:"leaf_hash"`
	RequestFencing  bool   `json:"request_fencing"`
}
