package sevenzipmanifest

import "runtime"

const Version = "26.02"

const ReleaseBaseURL = "https://github.com/ip7z/7zip/releases/download/" + Version

type File struct {
	SourceName string
	Filename   string
	SHA256     string
	Executable bool
}

type Artifact struct {
	Target string
	Asset  string
	SHA256 string
	Format string
	Files  []File
}

var artifacts = map[string]Artifact{
	"darwin-amd64": {
		Target: "darwin-amd64", Asset: "7z2602-mac.tar.xz", SHA256: "1cf6760579502f87e591ff5c73a005ec50b3e4d6f507e8b038382d563c3175b9", Format: "tar.xz",
		Files: []File{
			{SourceName: "7zz", Filename: "7zz", SHA256: "9c56cf3379a0d8544e9244958b96fdc7c17f9ce70f5a160eb2b41f5f3df96d8c", Executable: true},
			{SourceName: "License.txt", Filename: "License.txt", SHA256: "1790374e5352329cedb46ee3808930a88e9ca2f08b82b10fcf5cf605d2c301b1"},
		},
	},
	"darwin-arm64": {
		Target: "darwin-arm64", Asset: "7z2602-mac.tar.xz", SHA256: "1cf6760579502f87e591ff5c73a005ec50b3e4d6f507e8b038382d563c3175b9", Format: "tar.xz",
		Files: []File{
			{SourceName: "7zz", Filename: "7zz", SHA256: "9c56cf3379a0d8544e9244958b96fdc7c17f9ce70f5a160eb2b41f5f3df96d8c", Executable: true},
			{SourceName: "License.txt", Filename: "License.txt", SHA256: "1790374e5352329cedb46ee3808930a88e9ca2f08b82b10fcf5cf605d2c301b1"},
		},
	},
	"linux-amd64": {
		Target: "linux-amd64", Asset: "7z2602-linux-x64.tar.xz", SHA256: "41aaba7b1235304ab5aa0624530c67ae829496cd29e875925271efdccc28c03e", Format: "tar.xz",
		Files: []File{
			{SourceName: "7zz", Filename: "7zz", SHA256: "1676a968815b92e865bc0ffeecee3fa284ba4402bf23dc2bec2412c4b502e922", Executable: true},
			{SourceName: "License.txt", Filename: "License.txt", SHA256: "1790374e5352329cedb46ee3808930a88e9ca2f08b82b10fcf5cf605d2c301b1"},
		},
	},
	"linux-arm64": {
		Target: "linux-arm64", Asset: "7z2602-linux-arm64.tar.xz", SHA256: "70ea6cc737ae1495ea2d7eb20ef3120fe579bd3f1a83a9d2362b62ec5bde2bba", Format: "tar.xz",
		Files: []File{
			{SourceName: "7zz", Filename: "7zz", SHA256: "41ca798f0c0652c435cbdd9c3ba49d703c9410c597f40a5cd336304b3964c674", Executable: true},
			{SourceName: "License.txt", Filename: "License.txt", SHA256: "1790374e5352329cedb46ee3808930a88e9ca2f08b82b10fcf5cf605d2c301b1"},
		},
	},
	"windows-amd64": {
		Target: "windows-amd64", Asset: "7z2602-x64.exe", SHA256: "6745fa76dc2ea031596d8678f6f6b99c3c1b435b4164a63485adbbc7b8d82ef0", Format: "windows-installer",
		Files: []File{
			{SourceName: "7z.exe", Filename: "7z.exe", SHA256: "83967f1b02b43c4efeda302795722c809e0e81b8307de73558d10484d5676a7d", Executable: true},
			{SourceName: "7z.dll", Filename: "7z.dll", SHA256: "69fd4df057985c40e510e2fac182881c7f85e90aa13ec703f763a8fdb2ce61f8"},
			{SourceName: "License.txt", Filename: "License.txt", SHA256: "519ac0a4bded9c18ea02e0afb71f663d8c47373bd9facd3ac96a79f51d77765d"},
		},
	},
	"windows-arm64": {
		Target: "windows-arm64", Asset: "7z2602-arm64.exe", SHA256: "7c6fde79ed5e11b81c7bb6573b7962d3b6322aa5fce69c33ed19f672b55173ab", Format: "windows-installer",
		Files: []File{
			{SourceName: "7z.exe", Filename: "7z.exe", SHA256: "46009c25732880c9d49032ec20da46dfdc669fb60257f50308a0026b4fac3aef", Executable: true},
			{SourceName: "7z.dll", Filename: "7z.dll", SHA256: "7346eaea5f333b1d65b6b4eedf6797c416bbc91c75e46159df38aa28e153f7c5"},
			{SourceName: "License.txt", Filename: "License.txt", SHA256: "519ac0a4bded9c18ea02e0afb71f663d8c47373bd9facd3ac96a79f51d77765d"},
		},
	},
}

func For(goos, goarch string) (Artifact, bool) {
	artifact, found := artifacts[goos+"-"+goarch]
	return artifact, found
}

func Current() (Artifact, bool) {
	return For(runtime.GOOS, runtime.GOARCH)
}

func All() []Artifact {
	return []Artifact{
		artifacts["darwin-amd64"], artifacts["darwin-arm64"],
		artifacts["linux-amd64"], artifacts["linux-arm64"],
		artifacts["windows-amd64"], artifacts["windows-arm64"],
	}
}
