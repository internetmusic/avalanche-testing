package ava_services

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"github.com/docker/go-connections/nat"
	"github.com/kurtosis-tech/kurtosis/commons/services"
	"github.com/palantir/stacktrace"
	"github.com/sirupsen/logrus"
	"math/big"
	mathrand "math/rand"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	httpPort = 9650
	stakingPort = 9651
	stakingTlsCertPath = "/data/service/node.crt"
	stakingTlsKeyPath = "/data/service/node.key"
	maxCerts = 4000
	certificatePreamble = "CERTIFICATE"
	privateKeyPreamble = "RSA PRIVATE KEY"
)

// ================= Service ==================================

type GeckoService struct {
	ipAddr string
}

func (g GeckoService) GetStakingSocket() services.ServiceSocket {
	stakingPort, err := nat.NewPort("tcp", strconv.Itoa(stakingPort))
	if err != nil {
		// Realllllly don't think we should deal with propagating this one.... it means the user mistyped an integer
		panic(err)
	}
	return *services.NewServiceSocket(g.ipAddr, stakingPort)
}

func (g GeckoService) GetJsonRpcSocket() services.ServiceSocket {
	httpPort, err := nat.NewPort("tcp", strconv.Itoa(httpPort))
	if err != nil {
		panic(err)
	}
	return *services.NewServiceSocket(g.ipAddr, httpPort)
}


// ================ Initializer Core =============================
type geckoLogLevel string
const (
	LOG_LEVEL_VERBOSE geckoLogLevel = "verbo"
	LOG_LEVEL_DEBUG   geckoLogLevel = "debug"
	LOG_LEVEL_INFO    geckoLogLevel = "info"
)

type GeckoServiceInitializerCore struct {
	snowSampleSize    int
	snowQuorumSize    int
	stakingTlsEnabled bool
	logLevel          geckoLogLevel
}

func NewGeckoServiceInitializerCore(
	snowSampleSize int,
	snowQuorumSize int,
	stakingTlsEnabled bool,
	logLevel geckoLogLevel) *GeckoServiceInitializerCore {
	return &GeckoServiceInitializerCore{
		snowSampleSize:    snowSampleSize,
		snowQuorumSize:    snowQuorumSize,
		stakingTlsEnabled: stakingTlsEnabled,
		logLevel:          logLevel,
	}
}

func (g GeckoServiceInitializerCore) GetUsedPorts() map[int]bool {
	return map[int]bool{
		httpPort:    true,
		stakingPort: true,
	}
}


func (g GeckoServiceInitializerCore) GetFilepathsToMount() map[string]bool {
	if g.stakingTlsEnabled {
		return map[string]bool{
			stakingTlsCertPath: true,
			stakingTlsKeyPath: true,
		}
	}
	return make(map[string]bool)
}

func (g GeckoServiceInitializerCore) InitializeMountedFiles(osFiles map[string]*os.File, dependencies []services.Service) (err error) {
	/*
		TODO TODO TODO support >1 bootstrappers in staking mode by dynamically acquiring bootstrapper IDs instead of hardcoding one.
		For a staking network, there is only one bootstrapper. It has a hardcoded bootstrapperID that corresponds to its TLS cert.
		This must be hardcoded because Gecko requires specifying the bootstrapperID
		along with the bootstrapperIP when connecting to bootstrappers in TLS mode. There are two ways to get this, by
		knowing the ID ahead of time (hardcoding) and pinging the bootstrapper API once its up to get the IP.
		However we can not currently do this because the GetStartCommand code runs inside the initializer rather than
		inside the controller, therefore it is not in Docker, therefore it does not have network access to the bootstrapped node.
	 */

	certFilePointer := osFiles[stakingTlsCertPath]
	keyFilePointer := osFiles[stakingTlsKeyPath]
	if len(dependencies) == 0 {
		certFilePointer.WriteString(STAKER_1_CERT)
		keyFilePointer.WriteString(STAKER_1_PRIVATE_KEY)
	} else {
		rootCA := getRootCA()
		serviceCert := getServiceCert()
		certPEM, keyPEM, err := getServiceCertAndKeyFiles(serviceCert, rootCA)
		if err != nil {
			return stacktrace.Propagate(err, "Failed to write files.")
		}
		certFilePointer.Write(certPEM.Bytes())
		keyFilePointer.Write(keyPEM.Bytes())
	}
	return nil
}

func (g  GeckoServiceInitializerCore) GetStartCommand(publicIpAddr string, dependencies []services.Service) ([]string, error) {
	publicIpFlag := fmt.Sprintf("--public-ip=%s", publicIpAddr)
	commandList := []string{
		"/gecko/build/ava",
		publicIpFlag,
		"--network-id=local",
		fmt.Sprintf("--http-port=%d", httpPort),
		fmt.Sprintf("--staking-port=%d", stakingPort),
		fmt.Sprintf("--log-level=%s", g.logLevel),
		fmt.Sprintf("--snow-sample-size=%d", g.snowSampleSize),
		fmt.Sprintf("--snow-quorum-size=%d", g.snowQuorumSize),
		fmt.Sprintf("--staking-tls-enabled=%v", g.stakingTlsEnabled),
	}
	if g.stakingTlsEnabled {
		commandList = append(commandList, fmt.Sprintf("--staking-tls-cert-file=%s", stakingTlsCertPath))
		commandList = append(commandList, fmt.Sprintf("--staking-tls-key-file=%s", stakingTlsKeyPath))
	}


	// If bootstrap nodes are down then Gecko will wait until they are, so we don't actually need to busy-loop making
	// requests to the nodes
	if dependencies != nil && len(dependencies) > 0 {
		avaDependencies := make([]AvaService, 0, len(dependencies))
		for _, service := range dependencies {
			avaDependencies = append(avaDependencies, service.(AvaService))
		}

		socketStrs := make([]string, 0, len(avaDependencies))
		for _, service := range avaDependencies {
			socket := service.GetStakingSocket()
			socketStrs = append(socketStrs, fmt.Sprintf("%s:%d", socket.GetIpAddr(), socket.GetPort().Int()))
			if g.stakingTlsEnabled {
				// We hardcode the first bootstrapper ID from the TLS identities in gecko_service_tls_identities
				commandList = append(commandList, "--bootstrap-ids=" + STAKER_1_NODE_ID)
				// We currently have one cert -> ID mapping so break the for loop here.
				break
			}
		}
		joinedSockets := strings.Join(socketStrs, ",")
		commandList = append(commandList, "--bootstrap-ips=" + joinedSockets)
	}
	logrus.Debugf("Command list: %+v", commandList)
	return commandList, nil
}

func (g GeckoServiceInitializerCore) GetServiceFromIp(ipAddr string) services.Service {
	return GeckoService{ipAddr: ipAddr}
}


func getServiceCertAndKeyFiles(cert *x509.Certificate, ca *x509.Certificate) (certFile *bytes.Buffer, keyFile *bytes.Buffer, err error) {
	certPrivKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, stacktrace.Propagate(err, "Failed to generate random private key.")
	}
	certBytes, err := x509.CreateCertificate(rand.Reader, cert, ca, &(certPrivKey.PublicKey), certPrivKey)
	if err != nil {
		return nil, nil, stacktrace.Propagate(err, "Failed to sign service cert with cert authority.")
	}
	certPEM := new(bytes.Buffer)
	pem.Encode(certPEM, &pem.Block{
		Type:  certificatePreamble,
		Bytes: certBytes,
	})

	certPrivKeyPEM := new(bytes.Buffer)
	pem.Encode(certPrivKeyPEM, &pem.Block{
		Type: privateKeyPreamble,
		Bytes: x509.MarshalPKCS1PrivateKey(certPrivKey),
	})
	return certPEM, certPrivKeyPEM, nil
}

func getRootCA() *x509.Certificate {
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(2020),
		Subject: pkix.Name{
			Organization:  []string{"Kurtosis Technologies"},
			Country:       []string{"USA"},
			Province:      []string{""},
			Locality:      []string{""},
			StreetAddress: []string{""},
			PostalCode:    []string{""},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	return ca
}

func getServiceCert() *x509.Certificate {
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(int64(mathrand.Intn(maxCerts))),
		Subject: pkix.Name{
			Organization:  []string{"Kurtosis Technologies"},
			Country:       []string{"USA"},
			Province:      []string{""},
			Locality:      []string{""},
			StreetAddress: []string{""},
			PostalCode:    []string{""},
		},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		SubjectKeyId: []byte{1, 2, 3, 4, 6},
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	return cert
}