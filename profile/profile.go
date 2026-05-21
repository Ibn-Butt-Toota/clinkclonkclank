package profile

// Public profile struct for serving to public endpoints
type PublicProfile struct {
	StreamKey string `json:"streamKey"`
	IsActive  bool   `json:"isActive"`
}
