package app2

// Config defines configuration parameters for App
type Config struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	SockFile string `json:"sock_file"`
}
