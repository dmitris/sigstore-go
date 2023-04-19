package root

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/x509"
	_ "embed"
	"encoding/hex"
	"fmt"

	protocommon "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	prototrustroot "github.com/sigstore/protobuf-specs/gen/pb-go/trustroot/v1"
	protoverification "github.com/sigstore/protobuf-specs/gen/pb-go/verification/v1"
	"github.com/sigstore/sigstore/pkg/signature"
	"google.golang.org/protobuf/encoding/protojson"
)

const TrustedRootMediaType01 = "application/vnd.dev.sigstore.trustedroot+json;version=0.1"

type TrustedRoot struct {
	trustedRoot       *prototrustroot.TrustedRoot
	tlogVerifiers     map[string]signature.Verifier
	rootCerts         *x509.CertPool
	intermediateCerts *x509.CertPool
}

func NewTrustedRootFromProtobuf(trustedRoot *prototrustroot.TrustedRoot) (parsedTrustedRoot *TrustedRoot, err error) {
	if trustedRoot.GetMediaType() != TrustedRootMediaType01 {
		return nil, fmt.Errorf("unsupported TrustedRoot media type: %s", trustedRoot.GetMediaType())
	}

	parsedTrustedRoot = &TrustedRoot{trustedRoot: trustedRoot}
	parsedTrustedRoot.tlogVerifiers, err = ParseTlogVerifiers(trustedRoot)
	if err != nil {
		return nil, err
	}

	parsedTrustedRoot.rootCerts, parsedTrustedRoot.intermediateCerts, err = ParseCertificateAuthorities(trustedRoot)
	if err != nil {
		return nil, err
	}

	// TODO: Handle CT logs (trustedRoot.Ctlogs)
	// TODO: Handle TSA (trustedRoot.TimestampAuthorities)
	return parsedTrustedRoot, nil
}

func ParseTlogVerifiers(trustedRoot *prototrustroot.TrustedRoot) (tlogVerifiers map[string]signature.Verifier, err error) {
	tlogVerifiers = make(map[string]signature.Verifier)
	for _, tlog := range trustedRoot.GetTlogs() {
		if tlog.GetHashAlgorithm() != protocommon.HashAlgorithm_SHA2_256 {
			return nil, fmt.Errorf("unsupported tlog hash algorithm: %s", tlog.GetHashAlgorithm())
		}
		if tlog.GetLogId() == nil {
			return nil, fmt.Errorf("tlog missing log ID")
		}
		if tlog.GetLogId().GetKeyId() == nil {
			return nil, fmt.Errorf("tlog missing log ID key ID")
		}
		encodedKeyID := hex.EncodeToString(tlog.GetLogId().GetKeyId())

		if tlog.GetPublicKey() == nil {
			return nil, fmt.Errorf("tlog missing public key")
		}
		if tlog.GetPublicKey().GetRawBytes() == nil {
			return nil, fmt.Errorf("tlog missing public key raw bytes")
		}

		switch tlog.GetPublicKey().GetKeyDetails() {
		case protocommon.PublicKeyDetails_PKIX_ECDSA_P256_SHA_256:
			key, err := x509.ParsePKIXPublicKey(tlog.GetPublicKey().GetRawBytes())
			if err != nil {
				return nil, err
			}
			var ecKey *ecdsa.PublicKey
			var ok bool
			if ecKey, ok = key.(*ecdsa.PublicKey); !ok {
				return nil, fmt.Errorf("tlog public key is not ECDSA P256")
			}
			tlogVerifiers[encodedKeyID], err = signature.LoadECDSAVerifier(ecKey, crypto.SHA256)
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unsupported tlog public key type: %s", tlog.GetPublicKey().GetKeyDetails())
		}
		// TODO: Handle validity period (tlog.GetPublicKey().GetValidFor())
	}
	return tlogVerifiers, nil
}

func ParseCertificateAuthorities(trustedRoot *prototrustroot.TrustedRoot) (roots *x509.CertPool, intermediates *x509.CertPool, err error) {
	roots = x509.NewCertPool()
	intermediates = x509.NewCertPool()
	for _, ca := range trustedRoot.GetCertificateAuthorities() {
		if ca == nil {
			return nil, nil, fmt.Errorf("TrustedRoot CertificateAuthority is nil")
		}
		certChain := ca.GetCertChain()
		if certChain == nil {
			return nil, nil, fmt.Errorf("TrustedRoot CertificateAuthority missing cert chain")
		}
		chainLen := len(certChain.GetCertificates())
		if chainLen < 1 {
			return nil, nil, fmt.Errorf("TrustedRoot CertificateAuthority cert chain is empty")
		}

		for i, cert := range certChain.GetCertificates() {
			parsedCert, err := x509.ParseCertificate(cert.RawBytes)
			if err != nil {
				return nil, nil, err
			}
			if i == chainLen-1 {
				roots.AddCert(parsedCert)
			} else {
				intermediates.AddCert(parsedCert)
			}
		}

		// TODO: Should we inspect/enforce ca.Subject and ca.Uri?
		// TODO: Handle validity period (ca.ValidFor)
	}

	return roots, intermediates, nil
}

//go:embed trustroot.json
var trustedRootJSON []byte

// GetSigstoreTrustedRoot returns the Sigstore trusted root.
// TODO: Update to use TUF client
func GetSigstoreTrustedRoot() (*TrustedRoot, error) {
	pbTrustedRoot, err := GetSigstoreTrustedRootProtobuf()
	if err != nil {
		return nil, err
	}

	return NewTrustedRootFromProtobuf(pbTrustedRoot)
}

// GetSigstoreTrustedRootProtobuf returns the Sigstore trusted root as a protobuf.
func GetSigstoreTrustedRootProtobuf() (*prototrustroot.TrustedRoot, error) {
	pbTrustedRoot := &prototrustroot.TrustedRoot{}
	err := protojson.Unmarshal(trustedRootJSON, pbTrustedRoot)
	if err != nil {
		return nil, err
	}
	return pbTrustedRoot, nil
}

func GetDefaultOptions() *protoverification.ArtifactVerificationOptions {
	return &protoverification.ArtifactVerificationOptions{
		Signers: nil,
		TlogOptions: &protoverification.ArtifactVerificationOptions_TlogOptions{
			Threshold:                 1,
			PerformOnlineVerification: false,
			Disable:                   false,
		},
		CtlogOptions: &protoverification.ArtifactVerificationOptions_CtlogOptions{
			Threshold:   1,
			DetachedSct: false,
			Disable:     false,
		},
		TsaOptions: &protoverification.ArtifactVerificationOptions_TimestampAuthorityOptions{
			Threshold: 0,
			Disable:   true,
		},
	}
}
