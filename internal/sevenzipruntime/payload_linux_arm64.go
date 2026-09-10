//go:build linux && arm64

package sevenzipruntime

import (
	_ "embed"

	"github.com/hackycy/hackycy-cli/internal/sevenzipmanifest"
)

//go:embed payload/linux-arm64/7zz
var sevenZipExecutable []byte

//go:embed payload/linux-arm64/License.txt
var sevenZipLicense []byte

func currentPayload() Payload {
	artifact, _ := sevenzipmanifest.For("linux", "arm64")
	return Payload{Artifact: artifact, Files: []PayloadFile{{Metadata: artifact.Files[0], Bytes: sevenZipExecutable}, {Metadata: artifact.Files[1], Bytes: sevenZipLicense}}}
}
