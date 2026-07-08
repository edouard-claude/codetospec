// Command echoextractor is a test fixture implementing the external
// extractor protocol: it prints a facts JSON envelope on stdout.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"
)

func main() {
	mode := flag.String("mode", "ok", "ok | fail | hang | garbage")
	root := flag.String("root", "", "analyzed root, echoed in the facts")
	flag.Parse()

	switch *mode {
	case "fail":
		fmt.Fprintln(os.Stderr, "echoextractor: simulated failure")
		os.Exit(3)
	case "hang":
		time.Sleep(60 * time.Second)
	case "garbage":
		fmt.Println("this is not json")
	default:
		fmt.Printf(`{"schema":"codetospec/facts/v1","facts":[{"kind":"route","id":"route.get./ping","attrs":{"method":"GET","path":"/ping","root":%q},"source":{"path":"routes/web.php","lines":"1-1"},"origin":"echo","certainty":"proved"}]}`+"\n", *root)
	}
}
