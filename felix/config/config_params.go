// Copyright (c) 2020-2025 Tigera, Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package config

import (
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	v3 "github.com/projectcalico/api/pkg/apis/projectcalico/v3"
	"github.com/projectcalico/api/pkg/lib/numorstring"
	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/calico/felix/idalloc"
	"github.com/projectcalico/calico/felix/proto"
	"github.com/projectcalico/calico/libcalico-go/lib/apiconfig"
	"github.com/projectcalico/calico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/calico/libcalico-go/lib/names"
	"github.com/projectcalico/calico/libcalico-go/lib/set"
)

var (
	// RegexpIfaceElemRegexp matches an individual element in the overall interface list;
	// assumes the value represents a regular expression and is marked by '/' at the start
	// and end and cannot have spaces
	RegexpIfaceElemRegexp = regexp.MustCompile(`^/[^\s]+/$`)
	InterfaceRegex        = regexp.MustCompile("^[a-zA-Z0-9_.-]{1,15}$")
	// NonRegexpIfaceElemRegexp matches an individual element in the overall interface list;
	// assumes the value is between 1-15 chars long and only be alphanumeric or - or _
	NonRegexpIfaceElemRegexp = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,15}$`)
	IfaceListRegexp          = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,15}(,[a-zA-Z0-9_-]{1,15})*$`)
	AuthorityRegexp          = regexp.MustCompile(`^[^:/]+:\d+$`)
	HostnameRegexp           = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)
	StringRegexp             = regexp.MustCompile(`^.*$`)
	IfaceParamRegexp         = regexp.MustCompile(`^[a-zA-Z0-9:._+-]{1,15}$`)
	// Hostname  have to be valid ipv4, ipv6 or strings up to 64 characters.
	HostAddressRegexp = regexp.MustCompile(`^[a-zA-Z0-9:._+-]{1,64}$`)
)

// Source of a config value.  Values from higher-numbered sources override
// those from lower-numbered sources.  Note: some parameters (such as those
// needed to connect to the datastore) can only be set from a local source.
type Source uint8

const (
	Default Source = iota
	DatastoreGlobal
	DatastorePerHost
	ConfigFile
	EnvironmentVariable
	InternalOverride
)

// Default stats collection const used globally
const (
	DefaultAgeTimeout               = time.Duration(10) * time.Second
	DefaultInitialReportingDelay    = time.Duration(5) * time.Second
	DefaultExportingInterval        = time.Duration(1) * time.Second
	DefaultConntrackPollingInterval = time.Duration(5) * time.Second
)

var SourcesInDescendingOrder = []Source{InternalOverride, EnvironmentVariable, ConfigFile, DatastorePerHost, DatastoreGlobal}

func (source Source) String() string {
	switch source {
	case Default:
		return "<default>"
	case DatastoreGlobal:
		return "datastore (global)"
	case DatastorePerHost:
		return "datastore (per-host)"
	case ConfigFile:
		return "config file"
	case EnvironmentVariable:
		return "environment variable"
	case InternalOverride:
		return "internal override"
	}
	return fmt.Sprintf("<unknown(%v)>", uint8(source))
}

func (source Source) Local() bool {
	switch source {
	case Default, ConfigFile, EnvironmentVariable, InternalOverride:
		return true
	default:
		return false
	}
}

// Provider represents a particular provider or flavor of Kubernetes.
type Provider uint8

const (
	ProviderNone Provider = iota
	ProviderEKS
	ProviderGKE
	ProviderAKS
	ProviderOpenShift
	ProviderDockerEE
)

func (p Provider) String() string {
	switch p {
	case ProviderNone:
		return ""
	case ProviderEKS:
		return "EKS"
	case ProviderGKE:
		return "GKE"
	case ProviderAKS:
		return "AKS"
	case ProviderOpenShift:
		return "OpenShift"
	case ProviderDockerEE:
		return "DockerEnterprise"
	default:
		return fmt.Sprintf("<unknown-provider(%v)>", uint8(p))
	}
}

func newProvider(s string) (Provider, error) {
	switch strings.ToLower(s) {
	case strings.ToLower(ProviderNone.String()):
		return ProviderNone, nil
	case strings.ToLower(ProviderEKS.String()), "ecs":
		return ProviderEKS, nil
	case strings.ToLower(ProviderGKE.String()):
		return ProviderGKE, nil
	case strings.ToLower(ProviderAKS.String()):
		return ProviderAKS, nil
	case strings.ToLower(ProviderOpenShift.String()):
		return ProviderOpenShift, nil
	case strings.ToLower(ProviderDockerEE.String()):
		return ProviderDockerEE, nil
	default:
		return 0, fmt.Errorf("unknown provider %s", s)
	}
}

// Config contains the best, parsed config values loaded from the various sources.
// We use tags to control the parsing and validation.
type Config struct {
	// Configuration parameters.
	UseInternalDataplaneDriver bool          `config:"bool;true"`
	DataplaneDriver            string        `config:"file(must-exist,executable);calico-iptables-plugin;non-zero,die-on-fail,skip-default-validation"`
	DataplaneWatchdogTimeout   time.Duration `config:"seconds;90"`

	// Wireguard configuration
	WireguardEnabled               bool          `config:"bool;false"`
	WireguardEnabledV6             bool          `config:"bool;false"`
	WireguardListeningPort         int           `config:"int;51820"`
	WireguardListeningPortV6       int           `config:"int;51821"`
	WireguardRoutingRulePriority   int           `config:"int;99"`
	WireguardInterfaceName         string        `config:"iface-param;wireguard.cali;non-zero"`
	WireguardInterfaceNameV6       string        `config:"iface-param;wg-v6.cali;non-zero"`
	WireguardMTU                   int           `config:"int;0"`
	WireguardMTUV6                 int           `config:"int;0"`
	WireguardHostEncryptionEnabled bool          `config:"bool;false"`
	WireguardPersistentKeepAlive   time.Duration `config:"seconds;0"`
	WireguardThreadingEnabled      bool          `config:"bool;false"`

	// nftables configuration.
	NFTablesMode string `config:"oneof(Enabled,Disabled);Disabled"`

	// BPF configuration.
	BPFEnabled                         bool              `config:"bool;false"`
	BPFDisableUnprivileged             bool              `config:"bool;true"`
	BPFLogLevel                        string            `config:"oneof(off,info,debug);off;non-zero"`
	BPFConntrackLogLevel               string            `config:"oneof(off,debug);off;non-zero"`
	BPFConntrackCleanupMode            string            `config:"oneof(Auto,Userspace,BPFProgram);Auto"`
	BPFConntrackTimeouts               map[string]string `config:"keyvaluelist;CreationGracePeriod=10s,TCPSynSent=20s,TCPEstablished=1h,TCPFinsSeen=Auto,TCPResetSeen=40s,UDPTimeout=60s,GenericTimeout=10m,ICMPTimeout=5s"`
	BPFLogFilters                      map[string]string `config:"keyvaluelist;;"`
	BPFCTLBLogFilter                   string            `config:"oneof(all);;"`
	BPFDataIfacePattern                *regexp.Regexp    `config:"regexp;^((en|wl|ww|sl|ib)[Popsx].*|(eth|wlan|wwan|bond).*)"`
	BPFL3IfacePattern                  *regexp.Regexp    `config:"regexp;"`
	BPFConnectTimeLoadBalancingEnabled bool              `config:"bool;;"`
	BPFConnectTimeLoadBalancing        string            `config:"oneof(TCP,Enabled,Disabled);TCP;non-zero"`
	BPFHostNetworkedNATWithoutCTLB     string            `config:"oneof(Enabled,Disabled);Enabled;non-zero"`
	BPFExternalServiceMode             string            `config:"oneof(tunnel,dsr);tunnel;non-zero"`
	BPFDSROptoutCIDRs                  []string          `config:"cidr-list;;"`
	BPFKubeProxyIptablesCleanupEnabled bool              `config:"bool;true"`
	BPFKubeProxyMinSyncPeriod          time.Duration     `config:"seconds;1"`
	BPFKubeProxyEndpointSlicesEnabled  bool              `config:"bool;true"`
	BPFExtToServiceConnmark            int               `config:"int;0"`
	BPFPSNATPorts                      numorstring.Port  `config:"portrange;20000:29999"`
	BPFMapSizeNATFrontend              int               `config:"int;65536;non-zero"`
	BPFMapSizeNATBackend               int               `config:"int;262144;non-zero"`
	BPFMapSizeNATAffinity              int               `config:"int;65536;non-zero"`
	BPFMapSizeRoute                    int               `config:"int;262144;non-zero"`
	BPFMapSizeConntrack                int               `config:"int;512000;non-zero"`
	BPFMapSizePerCPUConntrack          int               `config:"int;0"`
	BPFMapSizeConntrackScaling         string            `config:"oneof(Disabled,DoubleIfFull);DoubleIfFull;non-zero"`
	BPFMapSizeConntrackCleanupQueue    int               `config:"int;100000;non-zero"`
	BPFMapSizeIPSets                   int               `config:"int;1048576;non-zero"`
	BPFMapSizeIfState                  int               `config:"int;1000;non-zero"`
	BPFHostConntrackBypass             bool              `config:"bool;false"`
	BPFEnforceRPF                      string            `config:"oneof(Disabled,Strict,Loose);Loose;non-zero"`
	BPFPolicyDebugEnabled              bool              `config:"bool;true"`
	BPFForceTrackPacketsFromIfaces     []string          `config:"iface-filter-slice;docker+"`
	BPFDisableGROForIfaces             *regexp.Regexp    `config:"regexp;"`
	BPFExcludeCIDRsFromNAT             []string          `config:"cidr-list;;"`
	BPFRedirectToPeer                  string            `config:"oneof(Disabled,Enabled,L2Only);L2Only;non-zero"`
	BPFAttachType                      string            `config:"oneof(tcx,tc);tcx;non-zero"`
	BPFExportBufferSizeMB              int               `config:"int;1;non-zero"`
	BPFProfiling                       string            `config:"oneof(Disabled,Enabled);Disabled;non-zero"`

	// DebugBPFCgroupV2 controls the cgroup v2 path that we apply the connect-time load balancer to.  Most distros
	// are configured for cgroup v1, which prevents all but the root cgroup v2 from working so this is only useful
	// for development right now.
	DebugBPFCgroupV2 string `config:"string;;local"`
	// DebugBPFMapRepinEnabled can be used to prevent Felix from repinning its BPF maps at startup.  This is useful for
	// testing with multiple Felix instances running on one host.
	DebugBPFMapRepinEnabled bool `config:"bool;false;local"`

	// DatastoreType controls which datastore driver Felix will use.  Typically, this is detected from the environment
	// and it does not need to be set manually. (For example, if `KUBECONFIG` is set, the kubernetes datastore driver
	// will be used by default).
	DatastoreType string `config:"oneof(kubernetes,etcdv3);etcdv3;non-zero,die-on-fail,local"`

	// FelixHostname is the name of this node, used to identify resources in the datastore that belong to this node.
	// Auto-detected from the node's hostname if not provided.
	FelixHostname string `config:"hostname;;local,non-zero"`

	// EtcdAddr: when using the `etcdv3` datastore driver, the etcd server and port to connect to.  If EtcdEndpoints
	// is also specified, it takes precedence.
	EtcdAddr string `config:"authority;127.0.0.1:2379;local"`
	// EtcdAddr: when using the `etcdv3` datastore driver, the URL scheme to use. If EtcdEndpoints
	// is also specified, it takes precedence.
	EtcdScheme string `config:"oneof(http,https);http;local"`
	// EtcdKeyFile: when using the `etcdv3` datastore driver, path to TLS private key file to use when connecting to
	// etcd.  If the key file is specified, the other TLS parameters are mandatory.
	EtcdKeyFile string `config:"file(must-exist);;local"`
	// EtcdCertFile: when using the `etcdv3` datastore driver, path to TLS certificate file to use when connecting to
	// etcd.  If the certificate file is specified, the other TLS parameters are mandatory.
	EtcdCertFile string `config:"file(must-exist);;local"`
	// EtcdCaFile: when using the `etcdv3` datastore driver, path to TLS CA file to use when connecting to
	// etcd.  If the CA file is specified, the other TLS parameters are mandatory.
	EtcdCaFile string `config:"file(must-exist);;local"`
	// EtcdEndpoints: when using the `etcdv3` datastore driver, comma-delimited list of etcd endpoints to connect to,
	// replaces EtcdAddr and EtcdScheme.
	EtcdEndpoints []string `config:"endpoint-list;;local"`

	// TyphaAddr if set, tells Felix to connect to Typha at the given address and port.  Overrides TyphaK8sServiceName.
	TyphaAddr string `config:"authority;;local"`
	// TyphaK8sServiceName if set, tells Felix to connect to Typha by looking up the Endpoints of the given Kubernetes
	// Service in namespace specified by TyphaK8sNamespace.
	TyphaK8sServiceName string `config:"string;;local"`
	// TyphaK8sNamespace namespace to look in when looking for Typha's service (see TyphaK8sServiceName).
	TyphaK8sNamespace string `config:"string;kube-system;non-zero,local"`
	// TyphaReadTimeout read timeout when reading from the Typha connection.  If typha sends no data for this long,
	// Felix will exit and restart.  (Note that Typha sends regular pings so traffic is always expected.)
	TyphaReadTimeout time.Duration `config:"seconds;30;local"`
	// TyphaWriteTimeout write timeout when writing data to Typha.
	TyphaWriteTimeout time.Duration `config:"seconds;10;local"`

	// TyphaKeyFile path to the TLS private key to use when communicating with Typha.  If this parameter is specified,
	// the other TLS parameters must also be specified.
	TyphaKeyFile string `config:"file(must-exist);;local"`
	// TyphaCertFile path to the TLS certificate to use when communicating with Typha.  If this parameter is specified,
	// the other TLS parameters must also be specified.
	TyphaCertFile string `config:"file(must-exist);;local"`
	// TyphaCAFile path to the TLS CA file to use when communicating with Typha.  If this parameter is specified,
	// the other TLS parameters must also be specified.
	TyphaCAFile string `config:"file(must-exist);;local"`
	// TyphaCN Common name to use when authenticating to Typha over TLS. If any TLS parameters are specified then one of
	// TyphaCN and TyphaURISAN must be set.
	TyphaCN string `config:"string;;local"`
	// TyphaURISAN URI SAN to use when authenticating to Typha over TLS. If any TLS parameters are specified then one of
	// TyphaCN and TyphaURISAN must be set.
	TyphaURISAN string `config:"string;;local"`

	Ipv6Support bool `config:"bool;true"`

	IptablesBackend                    string            `config:"oneof(legacy,nft,auto);auto"`
	RouteRefreshInterval               time.Duration     `config:"seconds;90"`
	InterfaceRefreshInterval           time.Duration     `config:"seconds;90"`
	DeviceRouteSourceAddress           net.IP            `config:"ipv4;"`
	DeviceRouteSourceAddressIPv6       net.IP            `config:"ipv6;"`
	DeviceRouteProtocol                int               `config:"int;3"`
	RemoveExternalRoutes               bool              `config:"bool;true"`
	ProgramClusterRoutes               string            `config:"oneof(Enabled,Disabled);Disabled"`
	IPForwarding                       string            `config:"oneof(Enabled,Disabled);Enabled"`
	IptablesRefreshInterval            time.Duration     `config:"seconds;180"`
	IptablesPostWriteCheckIntervalSecs time.Duration     `config:"seconds;5"`
	IptablesLockFilePath               string            `config:"file;/run/xtables.lock"`
	IptablesLockTimeoutSecs            time.Duration     `config:"seconds;0"`
	IptablesLockProbeIntervalMillis    time.Duration     `config:"millis;50"`
	FeatureDetectOverride              map[string]string `config:"keyvaluelist;;"`
	FeatureGates                       map[string]string `config:"keyvaluelist;;"`
	IpsetsRefreshInterval              time.Duration     `config:"seconds;90"`
	MaxIpsetSize                       int               `config:"int;1048576;non-zero"`
	XDPRefreshInterval                 time.Duration     `config:"seconds;90"`

	PolicySyncPathPrefix string `config:"file;;"`

	NetlinkTimeoutSecs time.Duration `config:"seconds;10"`

	MetadataAddr string `config:"hostname;127.0.0.1;die-on-fail"`
	MetadataPort int    `config:"int(0:65535);8775;die-on-fail"`

	OpenstackRegion string `config:"region;;die-on-fail"`

	InterfacePrefix  string           `config:"iface-list;cali;non-zero,die-on-fail"`
	InterfaceExclude []*regexp.Regexp `config:"iface-list-regexp;kube-ipvs0"`

	ChainInsertMode             string `config:"oneof(insert,append);insert;non-zero,die-on-fail"`
	DefaultEndpointToHostAction string `config:"oneof(DROP,RETURN,ACCEPT);DROP;non-zero,die-on-fail"`
	IptablesFilterAllowAction   string `config:"oneof(ACCEPT,RETURN);ACCEPT;non-zero,die-on-fail"`
	IptablesMangleAllowAction   string `config:"oneof(ACCEPT,RETURN);ACCEPT;non-zero,die-on-fail"`
	IptablesFilterDenyAction    string `config:"oneof(DROP,REJECT);DROP;non-zero,die-on-fail"`
	LogPrefix                   string `config:"string;calico-packet"`

	LogFilePath string `config:"file;/var/log/calico/felix.log;die-on-fail"`

	LogSeverityFile   string `config:"oneof(TRACE,DEBUG,INFO,WARNING,ERROR,FATAL);INFO"`
	LogSeverityScreen string `config:"oneof(TRACE,DEBUG,INFO,WARNING,ERROR,FATAL);INFO"`
	LogSeveritySys    string `config:"oneof(TRACE,DEBUG,INFO,WARNING,ERROR,FATAL);INFO"`
	// LogDebugFilenameRegex controls which source code files have their Debug log output included in the logs.
	// Only logs from files with names that match the given regular expression are included.  The filter only applies
	// to Debug level logs.
	LogDebugFilenameRegex *regexp.Regexp `config:"regexp(nil-on-empty);"`

	// Optional: VXLAN encap is now determined by the existing IP pools (Encapsulation struct)
	VXLANEnabled         *bool  `config:"*bool;"`
	VXLANPort            int    `config:"int;4789"`
	VXLANVNI             int    `config:"int;4096"`
	VXLANMTU             int    `config:"int;0"`
	VXLANMTUV6           int    `config:"int;0"`
	IPv4VXLANTunnelAddr  net.IP `config:"ipv4;"`
	IPv6VXLANTunnelAddr  net.IP `config:"ipv6;"`
	VXLANTunnelMACAddr   string `config:"string;"`
	VXLANTunnelMACAddrV6 string `config:"string;"`

	// Optional: IPIP encap is now determined by the existing IP pools (Encapsulation struct)
	IpInIpEnabled    *bool  `config:"*bool;"`
	IpInIpMtu        int    `config:"int;0"`
	IpInIpTunnelAddr net.IP `config:"ipv4;"`

	// Feature enablement.  Can be either "Enabled" or "Disabled".  Note, this governs the
	// programming of NAT mappings derived from Kubernetes pod annotations.  OpenStack floating
	// IPs are always programmed, regardless of this setting.
	FloatingIPs string `config:"oneof(Enabled,Disabled);Disabled"`

	// WindowsManageFirewallRules configures whether or not Felix will program Windows Firewall rules. [Default: Disabled]
	WindowsManageFirewallRules string `config:"oneof(Enabled,Disabled);Disabled"`

	// Knobs provided to explicitly control whether we add rules to drop encap traffic
	// from workloads. We always add them unless explicitly requested not to add them.
	AllowVXLANPacketsFromWorkloads bool `config:"bool;false"`
	AllowIPIPPacketsFromWorkloads  bool `config:"bool;false"`

	AWSSrcDstCheck string `config:"oneof(DoNothing,Enable,Disable);DoNothing;non-zero"`

	ServiceLoopPrevention string `config:"oneof(Drop,Reject,Disabled);Drop"`

	WorkloadSourceSpoofing string `config:"oneof(Disabled,Any);Disabled"`

	ReportingIntervalSecs time.Duration `config:"seconds;30"`
	ReportingTTLSecs      time.Duration `config:"seconds;90"`

	EndpointReportingEnabled   bool          `config:"bool;false"`
	EndpointReportingDelaySecs time.Duration `config:"seconds;1"`

	// EndpointStatusPathPrefix is the path to the directory
	// where endpoint status will be written. Endpoint status
	// file reporting is disabled if field is empty.
	//
	// Chosen directory should match the directory used by the CNI for PodStartupDelay.
	// [Default: "/var/run/calico"]
	EndpointStatusPathPrefix string `config:"file;/var/run/calico"`

	IptablesMarkMask uint32 `config:"mark-bitmask;0xffff0000;non-zero,die-on-fail"`

	DisableConntrackInvalidCheck bool `config:"bool;false"`

	HealthEnabled          bool                     `config:"bool;false"`
	HealthPort             int                      `config:"int(0:65535);9099"`
	HealthHost             string                   `config:"host-address;localhost"`
	HealthTimeoutOverrides map[string]time.Duration `config:"keydurationlist;;"`

	PrometheusMetricsEnabled          bool   `config:"bool;false"`
	PrometheusMetricsHost             string `config:"host-address;"`
	PrometheusMetricsPort             int    `config:"int(0:65535);9091"`
	PrometheusGoMetricsEnabled        bool   `config:"bool;true"`
	PrometheusProcessMetricsEnabled   bool   `config:"bool;true"`
	PrometheusWireGuardMetricsEnabled bool   `config:"bool;true"`

	FailsafeInboundHostPorts  []ProtoPort `config:"port-list;tcp:22,udp:68,tcp:179,tcp:2379,tcp:2380,tcp:5473,tcp:6443,tcp:6666,tcp:6667;die-on-fail"`
	FailsafeOutboundHostPorts []ProtoPort `config:"port-list;udp:53,udp:67,tcp:179,tcp:2379,tcp:2380,tcp:5473,tcp:6443,tcp:6666,tcp:6667;die-on-fail"`

	FlowLogsFlushInterval        time.Duration `config:"seconds;300"`
	FlowLogsCollectorDebugTrace  bool          `config:"bool;false"`
	FlowLogsGoldmaneServer       string        `config:"string;"`
	FlowLogsLocalReporter        string        `config:"oneof(Enabled,Disabled);Disabled"`
	FlowLogsPolicyEvaluationMode string        `config:"oneof(None,Continuous);Continuous"`

	KubeNodePortRanges    []numorstring.Port `config:"portrange-list;30000:32767"`
	NATPortRange          numorstring.Port   `config:"portrange;"`
	NATOutgoingAddress    net.IP             `config:"ipv4;"`
	NATOutgoingExclusions string             `config:"oneof(IPPoolsOnly,IPPoolsAndHostIPs);IPPoolsOnly"`

	UsageReportingEnabled          bool          `config:"bool;true"`
	UsageReportingInitialDelaySecs time.Duration `config:"seconds;300"`
	UsageReportingIntervalSecs     time.Duration `config:"seconds;86400"`
	ClusterGUID                    string        `config:"string;baddecaf"`
	ClusterType                    string        `config:"string;"`
	CalicoVersion                  string        `config:"string;"`

	ExternalNodesCIDRList []string `config:"cidr-list;;die-on-fail"`

	DebugMemoryProfilePath           string        `config:"file;;"`
	DebugCPUProfilePath              string        `config:"file;/tmp/felix-cpu-<timestamp>.pprof;"`
	DebugDisableLogDropping          bool          `config:"bool;false"`
	DebugSimulateCalcGraphHangAfter  time.Duration `config:"seconds;0"`
	DebugSimulateDataplaneHangAfter  time.Duration `config:"seconds;0"`
	DebugSimulateDataplaneApplyDelay time.Duration `config:"seconds;0"`
	DebugPanicAfter                  time.Duration `config:"seconds;0"`
	DebugSimulateDataRace            bool          `config:"bool;false"`
	// DebugHost is the host to bind the debug server port to.  Only used if DebugPort is non-zero.
	DebugHost string `config:"host-address;localhost"`
	// DebugPort is the port to bind the pprof debug server to or 0 to disable the debug port.
	DebugPort int `config:"int(0:65535);"`

	// Configure where Felix gets its routing information.
	// - workloadIPs: use workload endpoints to construct routes.
	// - calicoIPAM: use IPAM data to construct routes.
	RouteSource string `config:"oneof(WorkloadIPs,CalicoIPAM);CalicoIPAM"`

	// RouteTableRange is deprecated in favor of RouteTableRanges,
	RouteTableRange   idalloc.IndexRange   `config:"route-table-range;;die-on-fail"`
	RouteTableRanges  []idalloc.IndexRange `config:"route-table-ranges;;die-on-fail"`
	RouteSyncDisabled bool                 `config:"bool;false"`

	IptablesNATOutgoingInterfaceFilter string `config:"iface-param;"`

	SidecarAccelerationEnabled bool `config:"bool;false"`
	XDPEnabled                 bool `config:"bool;true"`
	GenericXDPEnabled          bool `config:"bool;false"`

	Variant string `config:"string;Calico"`

	// GoGCThreshold sets the Go runtime's GC threshold.  It is overridden by the GOGC env var if that is also
	// specified. A value of -1 disables GC.
	GoGCThreshold int `config:"int(-1);40"`
	// GoMemoryLimitMB sets the Go runtime's memory limit.  It is overridden by the GOMEMLIMIT env var if that is
	// also specified. A value of -1 disables the limit.
	GoMemoryLimitMB int `config:"int(-1);-1"`
	// GoMaxProcs sets the Go runtime's GOMAXPROCS.  It is overridden by the GOMAXPROCS env var if that is also
	// set. A value of -1 disables the override and uses the runtime default.
	GoMaxProcs int `config:"int(-1);-1"`

	// Configures MTU auto-detection.
	MTUIfacePattern *regexp.Regexp `config:"regexp;^((en|wl|ww|sl|ib)[Pcopsvx].*|(eth|wlan|wwan).*)"`

	// Encapsulation information calculated from IP Pools and FelixConfiguration (VXLANEnabled and IpInIpEnabled)
	Encapsulation Encapsulation

	// NftablesRefreshInterval controls the interval at which Felix periodically refreshes the nftables rules. [Default: 180s]
	NftablesRefreshInterval time.Duration `config:"seconds;180"`

	NftablesFilterAllowAction string `config:"oneof(ACCEPT,RETURN);ACCEPT;non-zero,die-on-fail"`
	NftablesMangleAllowAction string `config:"oneof(ACCEPT,RETURN);ACCEPT;non-zero,die-on-fail"`
	NftablesFilterDenyAction  string `config:"oneof(DROP,REJECT);DROP;non-zero,die-on-fail"`

	// MarkMask is the mask that Felix selects its nftables Mark bits from. Should be a 32 bit hexadecimal
	// number with at least 8 bits set, none of which clash with any other mark bits in use on the system.
	// [Default: 0xffff0000]
	NftablesMarkMask uint32 `config:"mark-bitmask;0xffff0000;non-zero,die-on-fail"`

	// State tracking.

	// internalOverrides contains our highest priority config source, generated from internal constraints
	// such as kernel version support.
	internalOverrides map[string]string
	// sourceToRawConfig maps each source to the set of config that was give to us via UpdateFrom.
	sourceToRawConfig map[Source]map[string]string
	// rawValues maps keys to the current highest-priority raw value.
	rawValues map[string]string
	// Err holds the most recent error from a config update.
	Err error

	loadClientConfigFromEnvironment func() (*apiconfig.CalicoAPIConfig, error)

	useNodeResourceUpdates bool

	RequireMTUFile bool `config:"bool;false"`
}

func (config *Config) FilterAllowAction() string {
	if config.NFTablesMode == "Enabled" {
		return config.NftablesFilterAllowAction
	}
	return config.IptablesFilterAllowAction
}

func (config *Config) MangleAllowAction() string {
	if config.NFTablesMode == "Enabled" {
		return config.NftablesMangleAllowAction
	}
	return config.IptablesMangleAllowAction
}

func (config *Config) FilterDenyAction() string {
	if config.NFTablesMode == "Enabled" {
		return config.NftablesFilterDenyAction
	}
	return config.IptablesFilterDenyAction
}

func (config *Config) MarkMask() uint32 {
	if config.NFTablesMode == "Enabled" {
		return config.NftablesMarkMask
	}
	return config.IptablesMarkMask
}

func (config *Config) TableRefreshInterval() time.Duration {
	if config.NFTablesMode == "Enabled" {
		return config.NftablesRefreshInterval
	}
	return config.IptablesRefreshInterval
}

func (config *Config) FlowLogsLocalReporterEnabled() bool {
	return config.FlowLogsLocalReporter == "Enabled"
}

func (config *Config) FlowLogsEnabled() bool {
	return config.FlowLogsGoldmaneServer != "" ||
		config.FlowLogsLocalReporterEnabled()
}

func (config *Config) ProgramClusterRoutesEnabled() bool {
	return config.ProgramClusterRoutes == "Enabled"
}

// Copy makes a copy of the object.  Internal state is deep copied but config parameters are only shallow copied.
// This saves work since updates to the copy will trigger the config params to be recalculated.
func (config *Config) Copy() *Config {
	// Start by shallow-copying the object.
	cp := *config

	// Copy the internal state over as a deep copy.
	cp.internalOverrides = map[string]string{}
	for k, v := range config.internalOverrides {
		cp.internalOverrides[k] = v
	}

	cp.sourceToRawConfig = map[Source]map[string]string{}
	for k, v := range config.sourceToRawConfig {
		cp.sourceToRawConfig[k] = map[string]string{}
		for k2, v2 := range v {
			cp.sourceToRawConfig[k][k2] = v2
		}
	}

	cp.rawValues = map[string]string{}
	for k, v := range config.rawValues {
		cp.rawValues[k] = v
	}

	return &cp
}

// ProtoPort aliases the v3 type so that we pick up its JSON encoding, which is
// used by the documentation generator.
type ProtoPort = v3.ProtoPort

type ServerPort struct {
	IP   string
	Port uint16
}

func (config *Config) ToConfigUpdate() *proto.ConfigUpdate {
	var buf proto.ConfigUpdate

	buf.SourceToRawConfig = map[uint32]*proto.RawConfig{}
	for source, c := range config.sourceToRawConfig {
		kvs := map[string]string{}
		for k, v := range c {
			kvs[k] = v
		}
		buf.SourceToRawConfig[uint32(source)] = &proto.RawConfig{
			Source: source.String(),
			Config: kvs,
		}
	}

	buf.Config = map[string]string{}
	for k, v := range config.rawValues {
		buf.Config[k] = v
	}

	return &buf
}

func (config *Config) UpdateFromConfigUpdate(configUpdate *proto.ConfigUpdate) (changedFields set.Set[string], err error) {
	log.Debug("Updating configuration from calculation graph message.")
	config.sourceToRawConfig = map[Source]map[string]string{}
	for sourceInt, c := range configUpdate.GetSourceToRawConfig() {
		source := Source(sourceInt)
		config.sourceToRawConfig[source] = map[string]string{}
		for k, v := range c.GetConfig() {
			config.sourceToRawConfig[source][k] = v
		}
	}
	// Note: the ConfigUpdate also carries the rawValues, but we recalculate those by calling resolve(),
	// which tells us if anything changed as a result.
	return config.resolve()
}

// UpdateFrom parses and merges the rawData from one particular source into this config object.
// If there is a config value already loaded from a higher-priority source, then
// the new value will be ignored (after validation).
func (config *Config) UpdateFrom(rawData map[string]string, source Source) (changed bool, err error) {
	log.Infof("Merging in config from %v: %v", source, rawData)
	// Defensively take a copy of the raw data, in case we've been handed
	// a mutable map by mistake.
	rawDataCopy := make(map[string]string)
	for k, v := range rawData {
		if v == "" {
			log.WithFields(log.Fields{
				"name":   k,
				"source": source,
			}).Info("Ignoring empty configuration parameter. Use value 'none' if " +
				"your intention is to explicitly disable the default value.")
			continue
		}
		rawDataCopy[k] = v
	}
	config.sourceToRawConfig[source] = rawDataCopy

	changedFields, err := config.resolve()
	if err != nil {
		return
	}
	return changedFields.Len() > 0, nil
}

func (config *Config) IsLeader() bool {
	return config.Variant == "Calico"
}

func (config *Config) InterfacePrefixes() []string {
	return strings.Split(config.InterfacePrefix, ",")
}

func (config *Config) OpenstackActive() bool {
	if strings.Contains(strings.ToLower(config.ClusterType), "openstack") {
		// OpenStack is explicitly known to be present.  Newer versions of the OpenStack plugin
		// set this flag.
		log.Debug("Cluster type contains OpenStack")
		return true
	}
	// If we get here, either OpenStack isn't present or we're running against an old version
	// of the OpenStack plugin, which doesn't set the flag.  Use heuristics based on the
	// presence of the OpenStack-related parameters.
	if config.MetadataAddr != "" && config.MetadataAddr != "127.0.0.1" {
		log.Debug("OpenStack metadata IP set to non-default, assuming OpenStack active")
		return true
	}
	if config.MetadataPort != 0 && config.MetadataPort != 8775 {
		log.Debug("OpenStack metadata port set to non-default, assuming OpenStack active")
		return true
	}
	for _, prefix := range config.InterfacePrefixes() {
		if prefix == "tap" {
			log.Debug("Interface prefix list contains 'tap', assuming OpenStack")
			return true
		}
	}
	log.Debug("No evidence this is an OpenStack deployment; disabling OpenStack special-cases")
	return false
}

// KubernetesProvider attempts to parse the kubernetes provider, e.g. AKS out of the ClusterType.
// The ClusterType is a string which contains a set of comma-separated values in no particular order.
func (config *Config) KubernetesProvider() Provider {
	settings := strings.Split(config.ClusterType, ",")
	for _, s := range settings {
		p, err := newProvider(s)
		if err == nil {
			log.WithFields(log.Fields{"clusterType": config.ClusterType, "provider": p}).Debug(
				"detected a known kubernetes provider")
			return p
		}
	}

	log.WithField("clusterType", config.ClusterType).Debug(
		"failed to detect a known kubernetes provider, defaulting to none")
	return ProviderNone
}

func (config *Config) applyDefaults() {
	for _, param := range knownParams {
		param.setDefault(config)
	}
	hostname, err := names.Hostname()
	if err != nil {
		log.Warningf("Failed to get hostname from kernel, "+
			"trying HOSTNAME variable: %v", err)
		hostname = strings.ToLower(os.Getenv("HOSTNAME"))
	}
	config.FelixHostname = hostname
}

func (config *Config) resolve() (changedFields set.Set[string], err error) {
	log.Debug("Resolving configuration from different sources...")

	// Take a copy, so we can compare the final post-parsing results at the end.
	oldConfigCopy := config.Copy()

	// Start with fresh defaults.
	config.applyDefaults()

	newRawValues := make(map[string]string)
	// Map from lower-case version of name to the highest-priority source found so far.
	// We use the lower-case version of the name since we can calculate it both for
	// expected and "raw" parameters, which may be used by plugins.
	nameToSource := make(map[string]Source)
	for _, source := range SourcesInDescendingOrder {
	valueLoop:
		for rawName, rawValue := range config.sourceToRawConfig[source] {
			lowerCaseName := strings.ToLower(rawName)
			currentSource := nameToSource[lowerCaseName]
			param, ok := knownParams[lowerCaseName]
			if !ok {
				if source >= currentSource {
					// Stash the raw value in case it's useful for an external
					// dataplane driver.  Use the raw name since the driver may
					// want it.
					newRawValues[rawName] = rawValue
					nameToSource[lowerCaseName] = source
				}
				log.WithField("raw name", rawName).Info(
					"Ignoring unknown config param.")
				continue valueLoop
			}
			metadata := param.GetMetadata()
			name := metadata.Name
			if metadata.Local && !source.Local() {
				log.Warningf("Ignoring local-only configuration %v=%q from %v",
					name, rawValue, source)
				continue valueLoop
			}

			log.Infof("Parsing value for %v: %v (from %v)",
				name, rawValue, source)
			var value interface{}
			if strings.ToLower(rawValue) == "none" {
				// Special case: we allow a value of "none" to force the value to
				// the zero value for a field.  The zero value often differs from
				// the default value.  Typically, the zero value means "turn off
				// the feature".
				if metadata.NonZero {
					err = errors.New("non-zero field cannot be set to none")
					log.Errorf(
						"Failed to parse value for %v: %v from source %v. %v",
						name, rawValue, source, err)
					config.Err = err
					return
				}
				value = metadata.ZeroValue
				log.Infof("Value set to 'none', replacing with zero-value: %#v.",
					value)
			} else {
				value, err = param.Parse(rawValue)
				if err != nil {
					logCxt := log.WithError(err).WithField("source", source)
					if metadata.DieOnParseFailure {
						logCxt.Error("Invalid (required) config value.")
						config.Err = err
						return
					} else {
						logCxt.WithField("default", metadata.Default).Warn(
							"Replacing invalid value with default")
						value = metadata.Default
						err = nil
					}
				}
			}

			log.Infof("Parsed value for %v: %v (from %v)",
				name, value, source)
			if source < currentSource {
				log.Infof("Skipping config value for %v from %v; "+
					"already have a value from %v", name,
					source, currentSource)
				continue
			}
			field := reflect.ValueOf(config).Elem().FieldByName(name)
			field.Set(reflect.ValueOf(value))
			newRawValues[name] = rawValue
			nameToSource[lowerCaseName] = source
		}
	}

	changedFields = set.New[string]()
	kind := reflect.TypeOf(Config{})
	for ii := 0; ii < kind.NumField(); ii++ {
		field := kind.Field(ii)
		tag := field.Tag.Get("config")
		if tag == "" {
			continue
		}

		oldV := reflect.ValueOf(oldConfigCopy).Elem().Field(ii).Interface()
		newV := reflect.ValueOf(config).Elem().Field(ii).Interface()

		if SafeParamsEqual(oldV, newV) {
			continue
		}
		changedFields.Add(field.Name)
	}
	log.WithField("changedFields", changedFields).Debug("Calculated changed fields.")

	config.rawValues = newRawValues
	return
}

// SafeParamsEqual compares two values drawn from the types of our config fields.  For the most part
// it uses reflect.DeepEquals() but some types (such as regexps and IPs) are handled inline to avoid pitfalls.
func SafeParamsEqual(a any, b any) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	switch a := a.(type) {
	case *regexp.Regexp:
		b := b.(*regexp.Regexp)
		if (a == nil) || (b == nil) {
			return a == b
		}
		return a.String() == b.String()
	case []*regexp.Regexp:
		b := b.([]*regexp.Regexp)
		if len(a) != len(b) {
			return false
		}
		for i := 0; i < len(a); i++ {
			if (a[i] == nil) || (b[i] == nil) {
				if a[i] == b[i] {
					continue
				}
				return false
			}
			if a[i].String() != b[i].String() {
				return false
			}
		}
		return true
	case net.IP:
		// IP has its own Equal method.
		b := b.(net.IP)
		return a.Equal(b)
	}

	return reflect.DeepEqual(a, b)
}

func (config *Config) setBy(name string, source Source) bool {
	_, isSet := config.sourceToRawConfig[source][name]
	return isSet
}

func (config *Config) setByConfigFileOrEnvironment(name string) bool {
	return config.setBy(name, ConfigFile) || config.setBy(name, EnvironmentVariable)
}

func (config *Config) DatastoreConfig() apiconfig.CalicoAPIConfig {
	// We want Felix's datastore connection to be fully configurable using the same
	// CALICO_XXX_YYY (or just XXX_YYY) environment variables that work for any libcalico-go
	// client - for both the etcdv3 and KDD cases.  However, for the etcd case, Felix has for a
	// long time supported FELIX_XXXYYY environment variables, and we want those to keep working
	// too.

	// To achieve that, first build a CalicoAPIConfig using libcalico-go's
	// LoadClientConfigFromEnvironment - which means incorporating defaults and CALICO_XXX_YYY
	// and XXX_YYY variables.
	cfg, err := config.loadClientConfigFromEnvironment()
	if err != nil {
		log.WithError(err).Panic("Failed to create datastore config")
	}

	// Now allow FELIX_XXXYYY variables or XxxYyy config file settings to override that, in the
	// etcd case. Note that etcd options are set even if the DatastoreType isn't etcdv3.
	// This allows the user to rely the default DatastoreType being etcdv3 and still being able
	// to configure the other etcdv3 options. As of the time of this code change, the etcd options
	// have no affect if the DatastoreType is not etcdv3.

	// Datastore type, either etcdv3 or kubernetes
	if config.setByConfigFileOrEnvironment("DatastoreType") {
		log.Infof("Overriding DatastoreType from felix config to %s", config.DatastoreType)
		if config.DatastoreType == string(apiconfig.EtcdV3) {
			cfg.Spec.DatastoreType = apiconfig.EtcdV3
		} else if config.DatastoreType == string(apiconfig.Kubernetes) {
			cfg.Spec.DatastoreType = apiconfig.Kubernetes
		}
	}

	// Endpoints.
	if config.setByConfigFileOrEnvironment("EtcdEndpoints") && len(config.EtcdEndpoints) > 0 {
		log.Infof("Overriding EtcdEndpoints from felix config to %s", config.EtcdEndpoints)
		cfg.Spec.EtcdEndpoints = strings.Join(config.EtcdEndpoints, ",")
		cfg.Spec.DatastoreType = apiconfig.EtcdV3
	} else if config.setByConfigFileOrEnvironment("EtcdAddr") {
		etcdEndpoints := config.EtcdScheme + "://" + config.EtcdAddr
		log.Infof("Overriding EtcdEndpoints from felix config to %s", etcdEndpoints)
		cfg.Spec.EtcdEndpoints = etcdEndpoints
		cfg.Spec.DatastoreType = apiconfig.EtcdV3
	}
	// TLS.
	if config.setByConfigFileOrEnvironment("EtcdKeyFile") {
		log.Infof("Overriding EtcdKeyFile from felix config to %s", config.EtcdKeyFile)
		cfg.Spec.EtcdKeyFile = config.EtcdKeyFile
	}
	if config.setByConfigFileOrEnvironment("EtcdCertFile") {
		log.Infof("Overriding EtcdCertFile from felix config to %s", config.EtcdCertFile)
		cfg.Spec.EtcdCertFile = config.EtcdCertFile
	}
	if config.setByConfigFileOrEnvironment("EtcdCaFile") {
		log.Infof("Overriding EtcdCaFile from felix config to %s", config.EtcdCaFile)
		cfg.Spec.EtcdCACertFile = config.EtcdCaFile
	}

	if !(config.Encapsulation.IPIPEnabled || config.Encapsulation.VXLANEnabled || config.BPFEnabled) {
		// Polling k8s for node updates is expensive (because we get many superfluous
		// updates) so disable if we don't need it.
		log.Info("Encap disabled, disabling node poll (if KDD is in use).")
		cfg.Spec.K8sDisableNodePoll = true
	}
	return *cfg
}

// Validate() performs cross-field validation.
func (config *Config) Validate() (err error) {
	if config.FelixHostname == "" {
		err = errors.New("Failed to determine hostname")
	}

	if config.DatastoreType == "etcdv3" && len(config.EtcdEndpoints) == 0 {
		if config.EtcdScheme == "" {
			err = errors.New("EtcdEndpoints and EtcdScheme both missing")
		}
		if config.EtcdAddr == "" {
			err = errors.New("EtcdEndpoints and EtcdAddr both missing")
		}
	}

	// If any client-side TLS config parameters are specified, they _all_ must be - except that
	// either TyphaCN or TyphaURISAN may be left unset.
	if config.TyphaCAFile != "" ||
		config.TyphaCertFile != "" ||
		config.TyphaKeyFile != "" ||
		config.TyphaCN != "" ||
		config.TyphaURISAN != "" {
		// Some TLS config specified.
		if config.TyphaKeyFile == "" ||
			config.TyphaCertFile == "" ||
			config.TyphaCAFile == "" ||
			(config.TyphaCN == "" && config.TyphaURISAN == "") {
			err = errors.New("If any Felix-Typha TLS config parameters are specified," +
				" they _all_ must be" +
				" - except that either TyphaCN or TyphaURISAN may be left unset.")
		}
	}

	if err != nil {
		config.Err = err
	}
	return
}

var knownParams map[string]Param

func Params() map[string]Param {
	if knownParams == nil {
		loadParams()
	}
	return knownParams
}

func loadParams() {
	knownParams = make(map[string]Param)
	config := Config{}
	kind := reflect.TypeOf(config)
	metaRegexp := regexp.MustCompile(`^([^;(]+)(?:\(([^)]*)\))?;` +
		`([^;]*)(?:;` +
		`([^;]*))?$`)
	for ii := 0; ii < kind.NumField(); ii++ {
		field := kind.Field(ii)
		tag := field.Tag.Get("config")
		if tag == "" {
			continue
		}
		captures := metaRegexp.FindStringSubmatch(tag)
		if len(captures) == 0 {
			log.Panicf("Failed to parse metadata for config param %v", field.Name)
		}
		log.Debugf("%v: metadata captures: %#v", field.Name, captures)
		kind := captures[1]       // Type: "int|oneof|bool|port-list|..."
		kindParams := captures[2] // Parameters for the type: e.g. for oneof "http,https"
		defaultStr := captures[3] // Default value e.g "1.0"
		flags := captures[4]
		var param Param
		switch kind {
		case "bool":
			param = &BoolParam{}
		case "*bool":
			param = &BoolPtrParam{}
		case "int":
			intParam := &IntParam{}
			paramMin := math.MinInt
			paramMax := math.MaxInt
			if kindParams != "" {
				for _, r := range strings.Split(kindParams, ",") {
					minAndMax := strings.Split(r, ":")
					paramMin = mustParseOptionalInt(minAndMax[0], math.MinInt, field.Name)
					if len(minAndMax) == 2 {
						paramMax = mustParseOptionalInt(minAndMax[1], math.MinInt, field.Name)
					}
					intParam.Ranges = append(intParam.Ranges, MinMax{Min: paramMin, Max: paramMax})
				}
			} else {
				intParam.Ranges = []MinMax{{Min: paramMin, Max: paramMax}}
			}
			param = intParam
		case "int32":
			param = &Int32Param{}
		case "mark-bitmask":
			param = &MarkBitmaskParam{}
		case "float":
			param = &FloatParam{}
		case "seconds":
			paramMin := math.MinInt
			paramMax := math.MaxInt
			var err error
			if kindParams != "" {
				minAndMax := strings.Split(kindParams, ":")
				paramMin, err = strconv.Atoi(minAndMax[0])
				if err != nil {
					log.Panicf("Failed to parse min value for %v", field.Name)
				}
				paramMax, err = strconv.Atoi(minAndMax[1])
				if err != nil {
					log.Panicf("Failed to parse max value for %v", field.Name)
				}
			}
			param = &SecondsParam{Min: paramMin, Max: paramMax}
		case "millis":
			param = &MillisParam{}
		case "iface-list":
			param = &RegexpParam{
				Regexp: IfaceListRegexp,
				Msg:    "invalid Linux interface name",
			}
		case "iface-list-regexp":
			param = &RegexpPatternListParam{
				NonRegexpElemRegexp: NonRegexpIfaceElemRegexp,
				RegexpElemRegexp:    RegexpIfaceElemRegexp,
				Delimiter:           ",",
				Msg:                 "list contains invalid Linux interface name or regex pattern",
				Schema:              "Comma-delimited list of Linux interface names/regex patterns. Regex patterns must start/end with `/`.",
			}
		case "regexp":
			param = &RegexpPatternParam{
				Flags: strings.Split(kindParams, ","),
			}
		case "iface-param":
			param = &RegexpParam{
				Regexp: IfaceParamRegexp,
				Msg:    "invalid Linux interface parameter",
			}
		case "file":
			param = &FileParam{
				MustExist:  strings.Contains(kindParams, "must-exist"),
				Executable: strings.Contains(kindParams, "executable"),
			}
		case "authority":
			param = &RegexpParam{
				Regexp: AuthorityRegexp,
				Msg:    "invalid URL authority",
			}
		case "ipv4":
			param = &Ipv4Param{}
		case "ipv6":
			param = &Ipv6Param{}
		case "endpoint-list":
			param = &EndpointListParam{}
		case "port-list":
			param = &PortListParam{}
		case "portrange":
			param = &PortRangeParam{}
		case "portrange-list":
			param = &PortRangeListParam{}
		case "hostname":
			param = &RegexpParam{
				Regexp: HostnameRegexp,
				Msg:    "invalid hostname",
			}
		case "host-address":
			param = &RegexpParam{
				Regexp: HostAddressRegexp,
				Msg:    "invalid host address",
			}
		case "region":
			param = &RegionParam{}
		case "oneof":
			options := strings.Split(kindParams, ",")
			lowerCaseToCanon := make(map[string]string)
			for _, option := range options {
				lowerCaseToCanon[strings.ToLower(option)] = option
			}
			param = &OneofListParam{
				lowerCaseOptionsToCanonical: lowerCaseToCanon,
			}
		case "string":
			param = &RegexpParam{
				Regexp: StringRegexp,
				Msg:    "invalid string",
			}
		case "cidr-list":
			param = &CIDRListParam{}
		case "server-list":
			param = &ServerListParam{}
		case "string-slice":
			param = &StringSliceParam{}
		case "interface-name-slice":
			param = &StringSliceParam{ValidationRegex: InterfaceRegex}
		case "iface-filter-slice":
			param = &StringSliceParam{ValidationRegex: IfaceParamRegexp}
		case "route-table-range":
			param = &RouteTableRangeParam{}
		case "route-table-ranges":
			param = &RouteTableRangesParam{}
		case "keyvaluelist":
			param = &KeyValueListParam{}
		case "keydurationlist":
			param = &KeyDurationListParam{}
		default:
			log.Panicf("Unknown type of parameter: %v", kind)
			panic("Unknown type of parameter") // Unreachable, keep the linter happy.
		}

		metadata := param.GetMetadata()
		metadata.Name = field.Name
		metadata.Type = field.Type.String()
		metadata.ZeroValue = reflect.ValueOf(config).FieldByName(field.Name).Interface()
		if strings.Contains(flags, "non-zero") {
			metadata.NonZero = true
		}
		if strings.Contains(flags, "die-on-fail") {
			metadata.DieOnParseFailure = true
		}
		if strings.Contains(flags, "local") {
			metadata.Local = true
		}

		if defaultStr != "" {
			metadata.DefaultString = defaultStr
			if strings.Contains(flags, "skip-default-validation") {
				metadata.Default = defaultStr
			} else {
				// Parse the default value and save it in the metadata. Doing
				// that here ensures that we syntax-check the defaults now.
				defaultVal, err := param.Parse(defaultStr)
				if err != nil {
					log.Panicf("Invalid default value: %v", err)
				}
				metadata.Default = defaultVal
			}
		} else {
			metadata.Default = metadata.ZeroValue
		}
		knownParams[strings.ToLower(field.Name)] = param
	}
}

// mustParseOptionalInt returns defaultVal if the given value is empty, otherwise parses the value as an int.
// Panics if the value is not a valid int.
func mustParseOptionalInt(rawValue string, defaultVal int, fieldName string) int {
	rawValue = strings.TrimSpace(rawValue)
	if rawValue == "" {
		return defaultVal
	}
	value, err := strconv.Atoi(rawValue)
	if err != nil {
		log.Panicf("Failed to parse value %q for %v", rawValue, fieldName)
	}
	return value
}

func (config *Config) SetUseNodeResourceUpdates(b bool) {
	config.useNodeResourceUpdates = b
}

func (config *Config) UseNodeResourceUpdates() bool {
	return config.useNodeResourceUpdates
}

func (config *Config) RawValues() map[string]string {
	cp := map[string]string{}
	for k, v := range config.rawValues {
		cp[k] = v
	}
	return cp
}

func (config *Config) SetLoadClientConfigFromEnvironmentFunction(fnc func() (*apiconfig.CalicoAPIConfig, error)) {
	config.loadClientConfigFromEnvironment = fnc
}

// OverrideParam installs a maximum priority parameter override for the given parameter.  This is useful for
// disabling features that are found to be unsupported, for example. By using an extra priority class, the
// override will persist even if the host/global config is updated.
func (config *Config) OverrideParam(name, value string) (bool, error) {
	config.internalOverrides[name] = value
	return config.UpdateFrom(config.internalOverrides, InternalOverride)
}

// RouteTableIndices compares provided args for the deprecated RoutTableRange arg
// and the newer RouteTableRanges arg, giving precedence to the newer arg if it's explicitly-set
func (config *Config) RouteTableIndices() []idalloc.IndexRange {
	if len(config.RouteTableRanges) == 0 {
		if config.RouteTableRange != (idalloc.IndexRange{}) {
			log.Warn("Proceeding with `RouteTableRange` config option. This field has been deprecated in favor of `RouteTableRanges`.")
			return []idalloc.IndexRange{
				config.RouteTableRange,
			}
		}

		// default RouteTableRanges val
		return []idalloc.IndexRange{
			{Min: clientv3.DefaultFelixRouteTableRangeMin, Max: clientv3.DefaultFelixRouteTableRangeMax},
		}
	} else if config.RouteTableRange != (idalloc.IndexRange{}) {
		log.Warn("Both `RouteTableRanges` and deprecated `RouteTableRange` options are set. `RouteTableRanges` value will be given precedence.")
	}
	return config.RouteTableRanges
}

func New() *Config {
	if knownParams == nil {
		loadParams()
	}
	p := &Config{
		rawValues:         map[string]string{},
		sourceToRawConfig: map[Source]map[string]string{},
		internalOverrides: map[string]string{},
	}
	p.loadClientConfigFromEnvironment = apiconfig.LoadClientConfigFromEnvironment
	p.applyDefaults()

	return p
}

type Param interface {
	GetMetadata() *Metadata
	Parse(raw string) (result interface{}, err error)
	setDefault(*Config)
	SchemaDescription() string
}

func FromConfigUpdate(msg *proto.ConfigUpdate) *Config {
	p := New()
	_, err := p.UpdateFromConfigUpdate(msg)
	if err != nil {
		log.WithError(err).Panic("Failed to convert ConfigUpdate back to Config.")
	}
	return p
}

type Encapsulation struct {
	IPIPEnabled    bool
	VXLANEnabled   bool
	VXLANEnabledV6 bool
}
