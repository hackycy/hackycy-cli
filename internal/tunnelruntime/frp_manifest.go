package tunnelruntime

import "fmt"

const FRPVersion = "0.70.1"

const frpDownloadBaseURL = "https://github.com/fatedier/frp/releases/download/v" + FRPVersion

// FRPArtifact is one fixed, official FRP input for a protocol-v3 target.
type FRPArtifact struct {
	Target      WireTarget
	Description FRPArtifactDescription
	FRPSSHA256  string
}

var frpArtifacts = []FRPArtifact{
	{
		Target: WireTarget{Platform: WirePlatformDarwin, Architecture: WireArchitectureARM64},
		Description: FRPArtifactDescription{
			Version: FRPVersion, Archive: "frp_0.70.1_darwin_arm64.tar.gz", URL: frpDownloadBaseURL + "/frp_0.70.1_darwin_arm64.tar.gz",
			SHA256: "cfa733b5a261c1647edee3c1fc4133d2542989b28f5602e81d47fc821d25c55f", FRPCSHA256: "dced7d6e9c35ecfd5a4625ddf3073660dd28e700387e7d838c64ef3cc1e4c1a9",
		},
		FRPSSHA256: "5ec9a8d3a25c117b737c9318c3d52805f829a61d8942411bda2f5f11d990416f",
	},
	{
		Target: WireTarget{Platform: WirePlatformDarwin, Architecture: WireArchitectureX64},
		Description: FRPArtifactDescription{
			Version: FRPVersion, Archive: "frp_0.70.1_darwin_amd64.tar.gz", URL: frpDownloadBaseURL + "/frp_0.70.1_darwin_amd64.tar.gz",
			SHA256: "cbf69cf26e5553e914e97d37f5d4367fa30f5f531d073a889465af4719281e25", FRPCSHA256: "32808dfdf91c4729f3c450d5a46afaa2fc293c8f6ee891743e3ca58685ba7a05",
		},
		FRPSSHA256: "1bc014d4f52b687c7bb27344273b1ae504ca7a992021feed1e8445b67d981ef6",
	},
	{
		Target: WireTarget{Platform: WirePlatformLinux, Architecture: WireArchitectureARM64},
		Description: FRPArtifactDescription{
			Version: FRPVersion, Archive: "frp_0.70.1_linux_arm64.tar.gz", URL: frpDownloadBaseURL + "/frp_0.70.1_linux_arm64.tar.gz",
			SHA256: "3990f396a9a490ee7f0e5f355287750ed41520064ed999eab443b5e9a78d773d", FRPCSHA256: "312be2787dc17c79b68ebf6cc9b536039b2fba595431782c68e3c056c1d491f8",
		},
		FRPSSHA256: "1930b2cf4ccf7b37834f2c88279d89c2aff5a177ecc307f77c483dbfe1adeb4a",
	},
	{
		Target: WireTarget{Platform: WirePlatformLinux, Architecture: WireArchitectureX64},
		Description: FRPArtifactDescription{
			Version: FRPVersion, Archive: "frp_0.70.1_linux_amd64.tar.gz", URL: frpDownloadBaseURL + "/frp_0.70.1_linux_amd64.tar.gz",
			SHA256: "333da23d1b9009d7c01638e9ba38cf4600f7d37d393f854e96ee1396adefa9a6", FRPCSHA256: "7d0270753bd171566a5389d2709fea29d2151f8fb4066ac20947e548e1da193a",
		},
		FRPSSHA256: "ed1dfde60fd9f6b10237b5ab5953a6d791072c9a378ff9d77d6dfb5f370be482",
	},
	{
		Target: WireTarget{Platform: WirePlatformWin32, Architecture: WireArchitectureARM64},
		Description: FRPArtifactDescription{
			Version: FRPVersion, Archive: "frp_0.70.1_windows_arm64.zip", URL: frpDownloadBaseURL + "/frp_0.70.1_windows_arm64.zip",
			SHA256: "74d3acaf0f03ee190dd0462f9b49861dca50b0559c5488af4b36572fc951fcca", FRPCSHA256: "66c6f031d36bed993d0b54ee2f6f834b85d01d8f502c42f62308a4368f5e8936",
		},
		FRPSSHA256: "29c7b664a6b2b12f0168c72bcca4c9ab19733ca58659cd944cd3b2555c4668df",
	},
	{
		Target: WireTarget{Platform: WirePlatformWin32, Architecture: WireArchitectureX64},
		Description: FRPArtifactDescription{
			Version: FRPVersion, Archive: "frp_0.70.1_windows_amd64.zip", URL: frpDownloadBaseURL + "/frp_0.70.1_windows_amd64.zip",
			SHA256: "531f3cd3cc41c0b4f077b54fe6b7dd83c0ff727e7f0bf412a4c78fa279165de5", FRPCSHA256: "1320325b3fd46d83ef7b2161d5e19f2b5dd9341b3391084a58d75ad82ef374d3",
		},
		FRPSSHA256: "9df8a65fe693de28a8fa4baf7189c44a354a34b94c31f4254e18cc26dea3c57f",
	},
}

// FRPArtifacts returns the complete immutable target manifest as values.
func FRPArtifacts() []FRPArtifact {
	return append([]FRPArtifact(nil), frpArtifacts...)
}

// ResolveFRPArtifact returns the one artifact available for target.
func ResolveFRPArtifact(target WireTarget) (FRPArtifact, error) {
	for _, artifact := range frpArtifacts {
		if artifact.Target == target {
			return artifact, nil
		}
	}
	return FRPArtifact{}, fmt.Errorf("%w: %s/%s", ErrUnsupportedPlatform, target.Platform, target.Architecture)
}

// CurrentFRPArtifact returns the fixed FRP artifact for the executing target.
func CurrentFRPArtifact() (FRPArtifact, error) {
	target, err := CurrentWireTarget()
	if err != nil {
		return FRPArtifact{}, err
	}
	return ResolveFRPArtifact(target)
}
