// Command forge-cert writes a loopback TLS certificate for Phase 5.
//
// ACME is not used: this machine has no public DNS. Point
// FORGE_TLS_CERT_FILE / FORGE_TLS_KEY_FILE at the files, then restart
// the API. The dashboard rewrite can stay HTTP on loopback; curl and a
// future proxy use HTTPS.
package main

import (
	"fmt"
	"os"

	"github.com/reyansh7/Forge/internal/tlscert"
)

func main() {
	dir := ".forge/tls"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	cert, key, err := tlscert.WriteLoopback(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("wrote", cert)
	fmt.Println("wrote", key)
	fmt.Println("set FORGE_TLS_CERT_FILE and FORGE_TLS_KEY_FILE, then restart the API")
}
