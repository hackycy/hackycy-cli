//go:build windows && amd64

package sevenzipruntime

import (
	_ "embed"

	"github.com/hackycy/hackycy-cli/internal/sevenzipmanifest"
)

//go:embed payload/windows-amd64/7z.exe
var sevenZipExecutable []byte

//go:embed payload/windows-amd64/7z.dll
var sevenZipDLL []byte

//go:embed payload/windows-amd64/License.txt
var sevenZipLicense []byte

func currentPayload() Payload {
	artifact, _ := sevenzipmanifest.For("windows", "amd64")
	return Payload{Artifact: artifact, Files: []PayloadFile{{Metadata: artifact.Files[0], Bytes: sevenZipExecutable}, {Metadata: artifact.Files[1], Bytes: sevenZipDLL}, {Metadata: artifact.Files[2], Bytes: sevenZipLicense}}}
}
