//go:build darwin && amd64

package sevenzipruntime

import (
	_ "embed"

	"github.com/hackycy/hackycy-cli/internal/sevenzipmanifest"
)

//go:embed payload/darwin-amd64/7zz
var sevenZipExecutable []byte

//go:embed payload/darwin-amd64/License.txt
var sevenZipLicense []byte

func currentPayload() Payload {
	artifact, _ := sevenzipmanifest.For("darwin", "amd64")
	return Payload{Artifact: artifact, Files: []PayloadFile{{Metadata: artifact.Files[0], Bytes: sevenZipExecutable}, {Metadata: artifact.Files[1], Bytes: sevenZipLicense}}}
}
