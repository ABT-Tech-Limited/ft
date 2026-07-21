package updater

import (
	"fmt"
	"net/http"
)

func clientWithHTTPSRedirects(client *http.Client) *http.Client {
	secureClient := *client
	originalCheckRedirect := client.CheckRedirect
	secureClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL == nil || req.URL.Scheme != "https" {
			return fmt.Errorf("redirect URL must use HTTPS")
		}
		if originalCheckRedirect != nil {
			return originalCheckRedirect(req, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		return nil
	}
	return &secureClient
}
