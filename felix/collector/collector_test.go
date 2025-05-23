//go:build !windows
// +build !windows

// Copyright (c) 2018-2025 Tigera, Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package collector

import (
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	kapiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/projectcalico/calico/app-policy/policystore"
	"github.com/projectcalico/calico/felix/calc"
	clttypes "github.com/projectcalico/calico/felix/collector/types"
	"github.com/projectcalico/calico/felix/collector/types/counter"
	"github.com/projectcalico/calico/felix/collector/types/metric"
	"github.com/projectcalico/calico/felix/collector/types/tuple"
	"github.com/projectcalico/calico/felix/collector/utils"
	"github.com/projectcalico/calico/felix/nfnetlink"
	"github.com/projectcalico/calico/felix/nfnetlink/nfnl"
	"github.com/projectcalico/calico/felix/proto"
	"github.com/projectcalico/calico/felix/rules"
	"github.com/projectcalico/calico/felix/types"
	"github.com/projectcalico/calico/lib/std/uniquelabels"
	"github.com/projectcalico/calico/libcalico-go/lib/backend/model"
	"github.com/projectcalico/calico/libcalico-go/lib/net"
)

const (
	ipv4       = 0x800
	proto_icmp = 1
	proto_tcp  = 6
	proto_udp  = 17
)

var (
	localIp1Str     = "10.0.0.1"
	localIp1        = utils.IpStrTo16Byte(localIp1Str)
	localNodeIp1Str = "192.168.180.1"
	localNodeIp1    = utils.IpStrTo16Byte(localNodeIp1Str)
	localIp2Str     = "10.0.0.2"
	localIp2        = utils.IpStrTo16Byte(localIp2Str)
	remoteIp1Str    = "20.0.0.1"
	remoteIp1       = utils.IpStrTo16Byte(remoteIp1Str)
	remoteIp2Str    = "20.0.0.2"
	remoteIp2       = utils.IpStrTo16Byte(remoteIp2Str)
	localIp1DNAT    = utils.IpStrTo16Byte("192.168.0.1")
	localIp2DNAT    = utils.IpStrTo16Byte("192.168.0.2")
	publicIP1Str    = "1.0.0.1"
	publicIP2Str    = "2.0.0.2"
	netSetIp1Str    = "8.8.8.8"
)

var (
	srcPort        = 54123
	srcPort2       = 54124
	serviceSrcPort = 456123
	nodeSrcPort    = 890123
	proxyPort      = 34754
	dstPort        = 80
	dstPortDNAT    = 8080
)

var (
	localWlEPKey1 = model.WorkloadEndpointKey{
		Hostname:       "localhost",
		OrchestratorID: "orchestrator",
		WorkloadID:     "localworkloadid1",
		EndpointID:     "localepid1",
	}

	localWlEPKey2 = model.WorkloadEndpointKey{
		Hostname:       "localhost",
		OrchestratorID: "orchestrator",
		WorkloadID:     "localworkloadid2",
		EndpointID:     "localepid2",
	}

	remoteWlEpKey1 = model.WorkloadEndpointKey{
		OrchestratorID: "orchestrator",
		WorkloadID:     "remoteworkloadid1",
		EndpointID:     "remoteepid1",
	}
	remoteWlEpKey2 = model.WorkloadEndpointKey{
		OrchestratorID: "orchestrator",
		WorkloadID:     "remoteworkloadid2",
		EndpointID:     "remoteepid2",
	}

	localWlEp1 = &model.WorkloadEndpoint{
		State:    "active",
		Name:     "cali1",
		Mac:      utils.MustParseMac("01:02:03:04:05:06"),
		IPv4Nets: []net.IPNet{utils.MustParseNet("10.0.0.1/32")},
		Labels: uniquelabels.Make(map[string]string{
			"id": "local-ep-1",
		}),
	}
	localWlEp2 = &model.WorkloadEndpoint{
		State:    "active",
		Name:     "cali2",
		Mac:      utils.MustParseMac("01:02:03:04:05:07"),
		IPv4Nets: []net.IPNet{utils.MustParseNet("10.0.0.2/32")},
		Labels: uniquelabels.Make(map[string]string{
			"id": "local-ep-2",
		}),
	}
	remoteWlEp1 = &model.WorkloadEndpoint{
		State:    "active",
		Name:     "cali3",
		Mac:      utils.MustParseMac("02:02:03:04:05:06"),
		IPv4Nets: []net.IPNet{utils.MustParseNet("20.0.0.1/32")},
		Labels: uniquelabels.Make(map[string]string{
			"id": "remote-ep-1",
		}),
	}
	remoteWlEp2 = &model.WorkloadEndpoint{
		State:    "active",
		Name:     "cali4",
		Mac:      utils.MustParseMac("02:03:03:04:05:06"),
		IPv4Nets: []net.IPNet{utils.MustParseNet("20.0.0.2/32")},
		Labels: uniquelabels.Make(map[string]string{
			"id": "remote-ep-2",
		}),
	}
	localEd1 = &calc.LocalEndpointData{
		CommonEndpointData: calc.CalculateCommonEndpointData(localWlEPKey1, localWlEp1),
		Ingress: &calc.MatchData{
			PolicyMatches: map[calc.PolicyID]int{
				{Name: "policy1", Tier: "default"}: 0,
				{Name: "policy2", Tier: "default"}: 0,
			},
			TierData: map[string]*calc.TierData{
				"default": {
					TierDefaultActionRuleID: calc.NewRuleID("default", "policy2", "", calc.RuleIndexTierDefaultAction,
						rules.RuleDirIngress, rules.RuleActionDeny),
					EndOfTierMatchIndex: 0,
				},
			},
			ProfileMatchIndex: 0,
		},
		Egress: &calc.MatchData{
			PolicyMatches: map[calc.PolicyID]int{
				{Name: "policy1", Tier: "default"}: 0,
				{Name: "policy2", Tier: "default"}: 0,
			},
			TierData: map[string]*calc.TierData{
				"default": {
					TierDefaultActionRuleID: calc.NewRuleID("default", "policy2", "", calc.RuleIndexTierDefaultAction,
						rules.RuleDirIngress, rules.RuleActionDeny),
					EndOfTierMatchIndex: 0,
				},
			},
			ProfileMatchIndex: 0,
		},
	}
	localEd2 = &calc.LocalEndpointData{
		CommonEndpointData: calc.CalculateCommonEndpointData(localWlEPKey2, localWlEp2),
		Ingress: &calc.MatchData{
			PolicyMatches: map[calc.PolicyID]int{
				{Name: "policy1", Tier: "default"}: 0,
				{Name: "policy2", Tier: "default"}: 0,
			},
			TierData: map[string]*calc.TierData{
				"default": {
					TierDefaultActionRuleID: calc.NewRuleID("default", "policy2", "", calc.RuleIndexTierDefaultAction,
						rules.RuleDirIngress, rules.RuleActionDeny),
					EndOfTierMatchIndex: 0,
				},
			},
			ProfileMatchIndex: 0,
		},
		Egress: &calc.MatchData{
			PolicyMatches: map[calc.PolicyID]int{
				{Name: "policy1", Tier: "default"}: 0,
				{Name: "policy2", Tier: "default"}: 0,
			},
			TierData: map[string]*calc.TierData{
				"default": {
					TierDefaultActionRuleID: calc.NewRuleID("default", "policy2", "", calc.RuleIndexTierDefaultAction,
						rules.RuleDirIngress, rules.RuleActionDeny),
					EndOfTierMatchIndex: 0,
				},
			},
			ProfileMatchIndex: 0,
		},
	}
	remoteEd1 = &calc.RemoteEndpointData{
		CommonEndpointData: calc.CalculateCommonEndpointData(remoteWlEpKey1, remoteWlEp1),
	}
	remoteEd2 = &calc.RemoteEndpointData{
		CommonEndpointData: calc.CalculateCommonEndpointData(remoteWlEpKey2, remoteWlEp2),
	}

	netSetKey1 = model.NetworkSetKey{
		Name: "dns-servers",
	}
	netSet1 = model.NetworkSet{
		Nets:   []net.IPNet{utils.MustParseNet(netSetIp1Str + "/32")},
		Labels: uniquelabels.Make(map[string]string{"public": "true"}),
	}

	svcKey1 = model.ResourceKey{
		Name:      "test-svc",
		Namespace: "test-namespace",
		Kind:      model.KindKubernetesService,
	}
	svc1 = kapiv1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "test-namespace"},
		Spec: kapiv1.ServiceSpec{
			ClusterIP: "10.10.10.10",
			ClusterIPs: []string{
				"10.10.10.10",
			},
			Ports: []kapiv1.ServicePort{
				{
					Name:       "nginx",
					Port:       80,
					TargetPort: intstr.IntOrString{Type: intstr.String, StrVal: "nginx"},
					Protocol:   kapiv1.ProtocolTCP,
				},
			},
		},
	}
)

// Nflog prefix test parameters
var (
	defTierAllowIngressNFLOGPrefix   = [64]byte{'A', 'P', 'I', '0', '|', 'd', 'e', 'f', 'a', 'u', 'l', 't', '.', 'p', 'o', 'l', 'i', 'c', 'y', '1'}
	defTierAllowEgressNFLOGPrefix    = [64]byte{'A', 'P', 'E', '0', '|', 'd', 'e', 'f', 'a', 'u', 'l', 't', '.', 'p', 'o', 'l', 'i', 'c', 'y', '1'}
	defTierDenyIngressNFLOGPrefix    = [64]byte{'D', 'P', 'I', '0', '|', 'd', 'e', 'f', 'a', 'u', 'l', 't', '.', 'p', 'o', 'l', 'i', 'c', 'y', '2'}
	defTierDenyEgressNFLOGPrefix     = [64]byte{'D', 'P', 'E', '0', '|', 'd', 'e', 'f', 'a', 'u', 'l', 't', '.', 'p', 'o', 'l', 'i', 'c', 'y', '2'}
	defTierPolicy1AllowIngressRuleID = &calc.RuleID{
		PolicyID: calc.PolicyID{
			Tier:      "default",
			Name:      "policy1",
			Namespace: "",
		},
		Index:     0,
		IndexStr:  "0",
		Action:    rules.RuleActionAllow,
		Direction: rules.RuleDirIngress,
	}
	defTierPolicy1AllowEgressRuleID = &calc.RuleID{
		PolicyID: calc.PolicyID{
			Tier:      "default",
			Name:      "policy1",
			Namespace: "",
		},
		Index:     0,
		IndexStr:  "0",
		Action:    rules.RuleActionAllow,
		Direction: rules.RuleDirEgress,
	}
	defTierPolicy2DenyIngressRuleID = &calc.RuleID{
		PolicyID: calc.PolicyID{
			Tier:      "default",
			Name:      "policy2",
			Namespace: "",
		},
		Index:     0,
		IndexStr:  "0",
		Action:    rules.RuleActionDeny,
		Direction: rules.RuleDirIngress,
	}
	defTierPolicy2DenyEgressRuleID = &calc.RuleID{
		PolicyID: calc.PolicyID{
			Tier:      "default",
			Name:      "policy2",
			Namespace: "",
		},
		Index:     0,
		IndexStr:  "0",
		Action:    rules.RuleActionDeny,
		Direction: rules.RuleDirEgress,
	}
	tier1TierPolicy1AllowIngressRuleID = &calc.RuleID{
		PolicyID: calc.PolicyID{
			Tier:      "tier1",
			Name:      "policy11",
			Namespace: "",
		},
		Index:     0,
		IndexStr:  "0",
		Action:    rules.RuleActionAllow,
		Direction: rules.RuleDirIngress,
	}
	tier1TierPolicy1DenyEgressRuleID = &calc.RuleID{
		PolicyID: calc.PolicyID{
			Tier:      "tier1",
			Name:      "policy11",
			Namespace: "",
		},
		Index:     0,
		IndexStr:  "0",
		Action:    rules.RuleActionDeny,
		Direction: rules.RuleDirEgress,
	}
)

var ingressPktAllowNflogTuple = nfnetlink.NflogPacketTuple{
	Src:   remoteIp1,
	Dst:   localIp1,
	Proto: proto_tcp,
	L4Src: nfnetlink.NflogL4Info{Port: srcPort},
	L4Dst: nfnetlink.NflogL4Info{Port: dstPort},
}

var ingressPktAllow = map[nfnetlink.NflogPacketTuple]*nfnetlink.NflogPacketAggregate{
	ingressPktAllowNflogTuple: {
		Prefixes: []nfnetlink.NflogPrefix{
			{
				Prefix:  defTierAllowIngressNFLOGPrefix,
				Len:     20,
				Bytes:   100,
				Packets: 1,
			},
		},
		Tuple: ingressPktAllowNflogTuple,
	},
}

var ingressPktAllowTuple = tuple.New(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)

var egressPktAllowNflogTuple = nfnetlink.NflogPacketTuple{
	Src:   localIp1,
	Dst:   remoteIp1,
	Proto: proto_udp,
	L4Src: nfnetlink.NflogL4Info{Port: srcPort},
	L4Dst: nfnetlink.NflogL4Info{Port: dstPort},
}
var egressPktAllow = map[nfnetlink.NflogPacketTuple]*nfnetlink.NflogPacketAggregate{
	egressPktAllowNflogTuple: {
		Prefixes: []nfnetlink.NflogPrefix{
			{
				Prefix:  defTierAllowEgressNFLOGPrefix,
				Len:     20,
				Bytes:   100,
				Packets: 1,
			},
		},
		Tuple: egressPktAllowNflogTuple,
	},
}
var egressPktAllowTuple = tuple.New(localIp1, remoteIp1, proto_udp, srcPort, dstPort)

var ingressPktDenyNflogTuple = nfnetlink.NflogPacketTuple{
	Src:   remoteIp1,
	Dst:   localIp1,
	Proto: proto_tcp,
	L4Src: nfnetlink.NflogL4Info{Port: srcPort},
	L4Dst: nfnetlink.NflogL4Info{Port: dstPort},
}

var ingressPktDeny = map[nfnetlink.NflogPacketTuple]*nfnetlink.NflogPacketAggregate{
	ingressPktDenyNflogTuple: {
		Prefixes: []nfnetlink.NflogPrefix{
			{
				Prefix:  defTierDenyIngressNFLOGPrefix,
				Len:     20,
				Bytes:   100,
				Packets: 1,
			},
		},
		Tuple: ingressPktDenyNflogTuple,
	},
}
var ingressPktDenyTuple = tuple.New(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)

var localPktIngressNflogTuple = nfnetlink.NflogPacketTuple{
	Src:   localIp1,
	Dst:   localIp2,
	Proto: proto_tcp,
	L4Src: nfnetlink.NflogL4Info{Port: srcPort},
	L4Dst: nfnetlink.NflogL4Info{Port: dstPort},
}
var localPktIngress = map[nfnetlink.NflogPacketTuple]*nfnetlink.NflogPacketAggregate{
	localPktIngressNflogTuple: {
		Prefixes: []nfnetlink.NflogPrefix{
			{
				Prefix:  defTierDenyIngressNFLOGPrefix,
				Len:     22,
				Bytes:   100,
				Packets: 1,
			},
		},
		Tuple: localPktIngressNflogTuple,
	},
}

var localPktIngressWithDNATNflogTuple = nfnetlink.NflogPacketTuple{
	Src:   localIp1,
	Dst:   localIp2,
	Proto: proto_tcp,
	L4Src: nfnetlink.NflogL4Info{Port: srcPort},
	L4Dst: nfnetlink.NflogL4Info{Port: dstPort},
}

var localPktIngressWithDNAT = map[nfnetlink.NflogPacketTuple]*nfnetlink.NflogPacketAggregate{
	localPktIngressWithDNATNflogTuple: {
		Prefixes: []nfnetlink.NflogPrefix{
			{
				Prefix:  defTierDenyIngressNFLOGPrefix,
				Len:     22,
				Bytes:   100,
				Packets: 1,
			},
		},
		Tuple: localPktIngressWithDNATNflogTuple,
		OriginalTuple: nfnetlink.CtTuple{
			Src:        localIp1,
			Dst:        localIp2DNAT,
			L3ProtoNum: ipv4,
			ProtoNum:   proto_tcp,
			L4Src:      nfnetlink.CtL4Src{Port: srcPort},
			L4Dst:      nfnetlink.CtL4Dst{Port: dstPortDNAT},
		},
		IsDNAT: true,
	},
}

var localPktEgressNflogTuple = nfnetlink.NflogPacketTuple{
	Src:   localIp1,
	Dst:   localIp2,
	Proto: proto_tcp,
	L4Src: nfnetlink.NflogL4Info{Port: srcPort},
	L4Dst: nfnetlink.NflogL4Info{Port: dstPort},
}
var localPktEgress = map[nfnetlink.NflogPacketTuple]*nfnetlink.NflogPacketAggregate{
	localPktEgressNflogTuple: {
		Prefixes: []nfnetlink.NflogPrefix{

			{
				Prefix:  defTierAllowEgressNFLOGPrefix,
				Len:     20,
				Bytes:   100,
				Packets: 1,
			},
		},
		Tuple: localPktEgressNflogTuple,
	},
}

var localPktEgressDenyTuplePreDNAT = tuple.New(localIp1, localIp1DNAT, proto_tcp, srcPort, dstPortDNAT)

var localPktEgressDeniedPreDNATNflogTuple = nfnetlink.NflogPacketTuple{
	Src:   localIp1,
	Dst:   localIp1DNAT,
	Proto: proto_tcp,
	L4Src: nfnetlink.NflogL4Info{Port: srcPort},
	L4Dst: nfnetlink.NflogL4Info{Port: dstPortDNAT},
}
var localPktEgressDeniedPreDNAT = map[nfnetlink.NflogPacketTuple]*nfnetlink.NflogPacketAggregate{
	localPktEgressDeniedPreDNATNflogTuple: {
		Prefixes: []nfnetlink.NflogPrefix{
			{
				Prefix:  defTierDenyEgressNFLOGPrefix,
				Len:     22,
				Bytes:   100,
				Packets: 1,
			},
		},
		Tuple:  localPktEgressDeniedPreDNATNflogTuple,
		IsDNAT: false,
	},
}

var localPktEgressAllowTuple = tuple.New(localIp1, remoteIp1, proto_tcp, srcPort, dstPort)

var localPktEgressAllowedPreDNATNflogTuple = nfnetlink.NflogPacketTuple{
	Src:   localIp1,
	Dst:   remoteIp1,
	Proto: proto_tcp,
	L4Src: nfnetlink.NflogL4Info{Port: srcPort},
	L4Dst: nfnetlink.NflogL4Info{Port: dstPort},
}
var localPktEgressAllowedPreDNAT = map[nfnetlink.NflogPacketTuple]*nfnetlink.NflogPacketAggregate{
	localPktEgressAllowedPreDNATNflogTuple: {
		Prefixes: []nfnetlink.NflogPrefix{
			{
				Prefix:  defTierAllowEgressNFLOGPrefix,
				Len:     22,
				Bytes:   100,
				Packets: 1,
			},
		},
		Tuple: localPktEgressAllowedPreDNATNflogTuple,
		OriginalTuple: nfnetlink.CtTuple{
			Src:        localIp1,
			Dst:        localIp1DNAT,
			L3ProtoNum: ipv4,
			ProtoNum:   proto_tcp,
			L4Src:      nfnetlink.CtL4Src{Port: srcPort},
			L4Dst:      nfnetlink.CtL4Dst{Port: dstPortDNAT},
		},
		IsDNAT: true,
	},
}

var _ = Describe("NFLOG Datasource", func() {
	Describe("NFLOG Incoming Packets", func() {
		// Inject info nflogChan
		var c *collector
		var lm *calc.LookupsCache
		var nflogReader *NFLogReader
		conf := &Config{
			AgeTimeout:            time.Duration(10) * time.Second,
			InitialReportingDelay: time.Duration(5) * time.Second,
			ExportingInterval:     time.Duration(1) * time.Second,
			FlowLogsFlushInterval: time.Duration(100) * time.Second,
			DisplayDebugTraceLogs: true,
		}
		BeforeEach(func() {
			epMap := map[[16]byte]calc.EndpointData{
				localIp1:  localEd1,
				localIp2:  localEd2,
				remoteIp1: remoteEd1,
			}
			nflogMap := map[[64]byte]*calc.RuleID{}

			for _, rid := range []*calc.RuleID{defTierPolicy1AllowEgressRuleID, defTierPolicy1AllowIngressRuleID, defTierPolicy2DenyIngressRuleID, defTierPolicy2DenyEgressRuleID} {
				nflogMap[policyIDStrToRuleIDParts(rid)] = rid
			}

			lm = newMockLookupsCache(epMap, nflogMap, nil, nil)
			nflogReader = NewNFLogReader(lm, 0, 0, 0, false)
			Expect(nflogReader.Start()).NotTo(HaveOccurred())
			c = newCollector(lm, conf).(*collector)
			c.SetPacketInfoReader(nflogReader)
			c.SetConntrackInfoReader(dummyConntrackInfoReader{})
			go func() {
				Expect(c.Start()).NotTo(HaveOccurred())
			}()
		})
		AfterEach(func() {
			nflogReader.Stop()
		})
		Describe("Test local destination", func() {
			It("should receive a single stat update with allow ruleid trace", func() {
				t := tuple.New(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)
				nflogReader.IngressC <- ingressPktAllow
				Eventually(c.epStats).Should(HaveKey(*t))
			})
		})
		Describe("Test local to local", func() {
			It("should receive a single stat update with deny ruleid trace", func() {
				t := tuple.New(localIp1, localIp2, proto_tcp, srcPort, dstPort)
				nflogReader.IngressC <- localPktIngress
				Eventually(c.epStats).Should(HaveKey(*t))
			})
		})
	})
})

// Entry remoteIp1:srcPort -> localIp1:dstPort
var inCtEntry = nfnetlink.CtEntry{
	OriginalTuple: nfnetlink.CtTuple{
		Src:        remoteIp1,
		Dst:        localIp1,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: srcPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: dstPort},
	},
	ReplyTuple: nfnetlink.CtTuple{
		Src:        localIp1,
		Dst:        remoteIp1,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: dstPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: srcPort},
	},
	OriginalCounters: nfnetlink.CtCounters{Packets: 1, Bytes: 100},
	ReplyCounters:    nfnetlink.CtCounters{Packets: 2, Bytes: 250},
	ProtoInfo:        nfnetlink.CtProtoInfo{State: nfnl.TCP_CONNTRACK_ESTABLISHED},
}

// Entry localIp1:srcPort -> localIp2:dstPort
var podProxyCTEntry = nfnetlink.CtEntry{
	OriginalTuple: nfnetlink.CtTuple{
		Src:        localIp1,
		Dst:        localIp2,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: srcPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: dstPort},
	},
	ReplyTuple: nfnetlink.CtTuple{
		Src:        localIp2,
		Dst:        localIp1,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: dstPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: srcPort},
	},
	Mark:             1024,
	OriginalCounters: nfnetlink.CtCounters{Packets: 1, Bytes: 100},
	ReplyCounters:    nfnetlink.CtCounters{Packets: 2, Bytes: 250},
	ProtoInfo:        nfnetlink.CtProtoInfo{State: nfnl.TCP_CONNTRACK_TIME_WAIT},
}

var proxyBackEndCTEntry = nfnetlink.CtEntry{
	OriginalTuple: nfnetlink.CtTuple{
		Src:        localIp1,
		Dst:        localIp2,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: proxyPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: dstPort},
	},
	ReplyTuple: nfnetlink.CtTuple{
		Src:        localIp2,
		Dst:        localIp1,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: dstPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: proxyPort},
	},
	Mark:             1024,
	OriginalCounters: nfnetlink.CtCounters{Packets: 1, Bytes: 100},
	ReplyCounters:    nfnetlink.CtCounters{Packets: 2, Bytes: 250},
	ProtoInfo:        nfnetlink.CtProtoInfo{State: nfnl.TCP_CONNTRACK_TIME_WAIT},
}

var podProxyEgressPktAllowNflogTuple = nfnetlink.NflogPacketTuple{
	Src:   localIp1,
	Dst:   localIp2,
	Proto: proto_tcp,
	L4Src: nfnetlink.NflogL4Info{Port: srcPort},
	L4Dst: nfnetlink.NflogL4Info{Port: dstPort},
}
var podProxyEgressPktAllow = map[nfnetlink.NflogPacketTuple]*nfnetlink.NflogPacketAggregate{
	podProxyEgressPktAllowNflogTuple: {
		Prefixes: []nfnetlink.NflogPrefix{
			{
				Prefix:  defTierAllowEgressNFLOGPrefix,
				Len:     20,
				Bytes:   100,
				Packets: 1,
			},
		},
		Tuple: podProxyEgressPktAllowNflogTuple,
	},
}

var proxyBackendIngressPktAllowNflogTuple = nfnetlink.NflogPacketTuple{
	Src:   localIp1,
	Dst:   localIp2,
	Proto: proto_tcp,
	L4Src: nfnetlink.NflogL4Info{Port: proxyPort},
	L4Dst: nfnetlink.NflogL4Info{Port: dstPort},
}
var proxyBackendIngressPktAllow = map[nfnetlink.NflogPacketTuple]*nfnetlink.NflogPacketAggregate{
	proxyBackendIngressPktAllowNflogTuple: {
		Prefixes: []nfnetlink.NflogPrefix{
			{
				Prefix:  defTierAllowIngressNFLOGPrefix,
				Len:     20,
				Bytes:   100,
				Packets: 1,
			},
		},
		Tuple: proxyBackendIngressPktAllowNflogTuple,
	},
}

func convertCtEntry(e nfnetlink.CtEntry, _ uint32) clttypes.ConntrackInfo {
	i, _ := ConvertCtEntryToConntrackInfo(e)
	return i
}

var outCtEntry = nfnetlink.CtEntry{
	OriginalTuple: nfnetlink.CtTuple{
		Src:        localIp1,
		Dst:        remoteIp1,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: srcPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: dstPort},
	},
	ReplyTuple: nfnetlink.CtTuple{
		Src:        remoteIp1,
		Dst:        localIp1,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: dstPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: srcPort},
	},
	OriginalCounters: nfnetlink.CtCounters{Packets: 1, Bytes: 100},
	ReplyCounters:    nfnetlink.CtCounters{Packets: 2, Bytes: 250},
	ProtoInfo:        nfnetlink.CtProtoInfo{State: nfnl.TCP_CONNTRACK_ESTABLISHED},
}

var outCtEntryWithSNAT = nfnetlink.CtEntry{
	OriginalTuple: nfnetlink.CtTuple{
		Src:        localIp1,
		Dst:        remoteIp1,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: srcPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: dstPort},
	},
	ReplyTuple: nfnetlink.CtTuple{
		Src:        remoteIp1,
		Dst:        localNodeIp1,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: dstPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: nodeSrcPort},
	},
	Status:           nfnl.IPS_SRC_NAT,
	OriginalCounters: nfnetlink.CtCounters{Packets: 1, Bytes: 100},
	ReplyCounters:    nfnetlink.CtCounters{Packets: 2, Bytes: 250},
	ProtoInfo:        nfnetlink.CtProtoInfo{State: nfnl.TCP_CONNTRACK_ESTABLISHED},
}

var outCtEntrySNATToServiceToSelf = nfnetlink.CtEntry{
	OriginalTuple: nfnetlink.CtTuple{
		Src:        localIp1,
		Dst:        remoteIp1,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: srcPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: serviceSrcPort},
	},
	ReplyTuple: nfnetlink.CtTuple{
		Src:        localIp1,
		Dst:        localNodeIp1,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: srcPort2},
		L4Dst:      nfnetlink.CtL4Dst{Port: srcPort},
	},
	Status:           nfnl.IPS_SRC_NAT | nfnl.IPS_DST_NAT,
	OriginalCounters: nfnetlink.CtCounters{Packets: 1, Bytes: 100},
	ReplyCounters:    nfnetlink.CtCounters{Packets: 2, Bytes: 250},
	ProtoInfo:        nfnetlink.CtProtoInfo{State: nfnl.TCP_CONNTRACK_ESTABLISHED},
}

var localCtEntry = nfnetlink.CtEntry{
	OriginalTuple: nfnetlink.CtTuple{
		Src:        localIp1,
		Dst:        localIp2,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: srcPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: dstPort},
	},
	ReplyTuple: nfnetlink.CtTuple{
		Src:        localIp2,
		Dst:        localIp1,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: dstPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: srcPort},
	},
	OriginalCounters: nfnetlink.CtCounters{Packets: 1, Bytes: 100},
	ReplyCounters:    nfnetlink.CtCounters{Packets: 2, Bytes: 250},
	ProtoInfo:        nfnetlink.CtProtoInfo{State: nfnl.TCP_CONNTRACK_ESTABLISHED},
}

// DNAT Conntrack Entries
// DNAT from localIp1DNAT:dstPortDNAT --> localIp1:dstPort
var inCtEntryWithDNAT = nfnetlink.CtEntry{
	OriginalTuple: nfnetlink.CtTuple{
		Src:        remoteIp1,
		Dst:        localIp1DNAT,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: srcPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: dstPortDNAT},
	},
	ReplyTuple: nfnetlink.CtTuple{
		Src:        localIp1,
		Dst:        remoteIp1,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: dstPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: srcPort},
	},
	Status:           nfnl.IPS_DST_NAT,
	OriginalCounters: nfnetlink.CtCounters{Packets: 1, Bytes: 100},
	ReplyCounters:    nfnetlink.CtCounters{Packets: 2, Bytes: 250},
	ProtoInfo:        nfnetlink.CtProtoInfo{State: nfnl.TCP_CONNTRACK_ESTABLISHED},
}

// DNAT from localIp2DNAT:dstPortDNAT --> localIp2:dstPort
var localCtEntryWithDNAT = nfnetlink.CtEntry{
	OriginalTuple: nfnetlink.CtTuple{
		Src:        localIp1,
		Dst:        localIp2DNAT,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: srcPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: dstPortDNAT},
	},
	ReplyTuple: nfnetlink.CtTuple{
		Src:        localIp2,
		Dst:        localIp1,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: dstPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: srcPort},
	},
	Status:           nfnl.IPS_DST_NAT,
	OriginalCounters: nfnetlink.CtCounters{Packets: 1, Bytes: 100},
	ReplyCounters:    nfnetlink.CtCounters{Packets: 2, Bytes: 250},
	ProtoInfo:        nfnetlink.CtProtoInfo{State: nfnl.TCP_CONNTRACK_ESTABLISHED},
}

var outCtEntryWithDNAT = nfnetlink.CtEntry{
	OriginalTuple: nfnetlink.CtTuple{
		Src:        localIp1,
		Dst:        localIp1DNAT,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: srcPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: dstPortDNAT},
	},
	ReplyTuple: nfnetlink.CtTuple{
		Src:        remoteIp1,
		Dst:        localIp1,
		L3ProtoNum: ipv4,
		ProtoNum:   proto_tcp,
		L4Src:      nfnetlink.CtL4Src{Port: dstPort},
		L4Dst:      nfnetlink.CtL4Dst{Port: srcPort},
	},
	Status:           nfnl.IPS_DST_NAT,
	OriginalCounters: nfnetlink.CtCounters{Packets: 1, Bytes: 100},
	ReplyCounters:    nfnetlink.CtCounters{Packets: 2, Bytes: 250},
	ProtoInfo:        nfnetlink.CtProtoInfo{State: nfnl.TCP_CONNTRACK_ESTABLISHED},
}

var _ = Describe("Conntrack Datasource", func() {
	var c *collector
	var ciReaderSenderChan chan []clttypes.ConntrackInfo
	// var piReaderInfoSenderChan chan PacketInfo
	var lm *calc.LookupsCache
	var epMapDelete map[[16]byte]calc.EndpointData
	var epMapSwapLocal map[[16]byte]calc.EndpointData
	var nflogReader *NFLogReader
	conf := &Config{
		AgeTimeout:            time.Duration(10) * time.Second,
		InitialReportingDelay: time.Duration(5) * time.Second,
		ExportingInterval:     time.Duration(1) * time.Second,
		FlowLogsFlushInterval: time.Duration(100) * time.Second,
		DisplayDebugTraceLogs: true,
	}
	BeforeEach(func() {
		epMap := map[[16]byte]calc.EndpointData{
			localIp1:  localEd1,
			localIp2:  localEd2,
			remoteIp1: remoteEd1,
		}
		epMapSwapLocal = map[[16]byte]calc.EndpointData{
			localIp1:  localEd2,
			localIp2:  localEd1,
			remoteIp1: remoteEd1,
		}
		epMapDelete = map[[16]byte]calc.EndpointData{
			localIp1:  nil,
			localIp2:  nil,
			remoteIp1: nil,
		}

		nflogMap := map[[64]byte]*calc.RuleID{}

		for _, rid := range []*calc.RuleID{defTierPolicy1AllowEgressRuleID, defTierPolicy1AllowIngressRuleID, defTierPolicy2DenyIngressRuleID, defTierPolicy2DenyEgressRuleID} {
			nflogMap[policyIDStrToRuleIDParts(rid)] = rid
		}

		lm = newMockLookupsCache(epMap, nflogMap, nil, nil)
		nflogReader = NewNFLogReader(lm, 0, 0, 0, false)
		c = newCollector(lm, conf).(*collector)

		c.SetPacketInfoReader(nflogReader)

		ciReaderSenderChan = make(chan []clttypes.ConntrackInfo, 1)
		c.SetConntrackInfoReader(dummyConntrackInfoReader{
			MockSenderChannel: ciReaderSenderChan,
		})

		Expect(c.Start()).NotTo(HaveOccurred())
	})

	Describe("Test local destination", func() {
		It("should create a single entry in inbound direction", func() {
			t := tuple.New(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)

			// will call handlerInfo from c.Start() in BeforeEach
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(inCtEntry, 0)}

			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			data := c.epStats[*t]
			Expect(data.ConntrackPacketsCounter()).Should(Equal(*counter.New(inCtEntry.OriginalCounters.Packets)))
			Expect(data.ConntrackPacketsCounterReverse()).Should(Equal(*counter.New(inCtEntry.ReplyCounters.Packets)))
			Expect(data.ConntrackBytesCounter()).Should(Equal(*counter.New(inCtEntry.OriginalCounters.Bytes)))
			Expect(data.ConntrackBytesCounterReverse()).Should(Equal(*counter.New(inCtEntry.ReplyCounters.Bytes)))
		})
		It("should handle destination becoming non-local by removing entry on next conntrack update for reported flow", func() {
			t := tuple.New(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)
			// will call handlerInfo from c.Start() in BeforeEach
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(inCtEntry, 0)}

			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			// Flag the data as reported, remove endpoints from mock data and send in CT entry again.
			data := c.epStats[*t]
			data.Reported = true
			lm.SetMockData(epMapDelete, nil, nil, nil)
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(inCtEntry, 0)}

			// This is a reported flow, and is a conntrack update - this should not impact the stored data at all.
			Consistently(c.epStats, "500ms", "100ms").Should(HaveKey(*t))
		})
		It("should handle destination becoming non-local by removing entry on next conntrack update for unreported flow", func() {
			t := tuple.New(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)
			// will call handlerInfo from c.Start() in BeforeEach
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(inCtEntry, 0)}

			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			// Data is not reported. Remove endpoints from mock data and send in CT entry again.
			lm.SetMockData(epMapDelete, nil, nil, nil)
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(inCtEntry, 0)}

			// This is an unreported flow, and is a conntrack update. We can update the endpoint, but we never downgrade
			// to having no endpoint (since we handle the situation where endpoint is deleted before we gather all
			// logs).
			Consistently(c.epStats, "500ms", "100ms").Should(HaveKey(*t))
		})
		It("should handle destination changing on next conntrack update for reported flow", func() {
			t := tuple.New(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)
			// will call handlerInfo from c.Start() in BeforeEach
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(inCtEntry, 0)}

			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			// Flag the data as reported, swap local endpoints from mock data and send in CT entry again.
			data := c.epStats[*t]
			data.Reported = true
			oldSrc := data.SrcEp
			oldDest := data.DstEp

			lm.SetMockData(epMapSwapLocal, nil, nil, nil)
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(inCtEntry, 0)}

			// This is a reported flow, and is a conntrack update - this should not impact the stored data at all since
			// the endpoint should not be changing for a constant connection.
			Consistently(c.epStats, "500ms", "100ms").Should(HaveKey(*t))
			Expect(data.SrcEp).To(Equal(oldSrc))
			Expect(data.DstEp).To(Equal(oldDest))
		})
		It("should handle destination changing on next conntrack update for unreported flow", func() {
			t := tuple.New(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)
			// will call handlerInfo from c.Start() in BeforeEach
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(inCtEntry, 0)}

			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			// Data is not reported. swap local endpoints from mock data and send in packetinfo entry again.
			data := c.epStats[*t]
			oldSrc := data.SrcEp
			oldDest := data.DstEp

			lm.SetMockData(epMapSwapLocal, nil, nil, nil)
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(inCtEntry, 0)}

			// This is an unreported flow, and is a conntrack update. We can update the endpoint.
			Consistently(c.epStats, "500ms", "100ms").Should(HaveKey(*t))
			Expect(data.SrcEp).To(Equal(oldSrc))
			Expect(data.DstEp).NotTo(Equal(oldDest))
		})
		It("should handle destination becoming non-local by removing entry on next packetinfo update for reported flow", func() {
			pktinfo := nflogReader.ConvertNflogPkt(rules.RuleDirIngress, ingressPktAllow[ingressPktAllowNflogTuple])
			c.applyPacketInfo(pktinfo)
			t := tuple.New(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)

			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			// Flag the data as reported, remove endpoints from mock data and send in packetinfo entry again.
			data := c.epStats[*t]
			data.Reported = true
			lm.SetMockData(epMapDelete, nil, nil, nil)
			c.applyPacketInfo(pktinfo)

			// This is a reported flow but we are going through packet processing still. It should be expired and
			// removed.
			Eventually(c.epStats, "500ms", "100ms").ShouldNot(HaveKey(*t))
			Expect(data.Reported).To(BeFalse())
		})
		It("should handle destination becoming non-local by removing entry on next packetinfo update for unreported flow", func() {
			pktinfo := nflogReader.ConvertNflogPkt(rules.RuleDirIngress, ingressPktAllow[ingressPktAllowNflogTuple])
			c.applyPacketInfo(pktinfo)
			t := tuple.New(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)

			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			// Data is not reported. Remove endpoints from mock data and send in packetinfo entry again.
			data := c.epStats[*t]
			oldSrc := data.SrcEp
			oldDest := data.DstEp
			lm.SetMockData(epMapDelete, nil, nil, nil)
			c.applyPacketInfo(pktinfo)

			// This is an unreported flow but we are going through packet processing still. However, since the endpoint
			// data has been removed assume it has just been deleted and don't downgrade our endpoint data.
			Consistently(c.epStats, "500ms", "100ms").Should(HaveKey(*t))
			Expect(data.Reported).To(BeFalse())
			Expect(data.SrcEp).To(Equal(oldSrc))
			Expect(data.DstEp).To(Equal(oldDest))
		})
		It("should handle destination changing on next packetinfo update for reported flow", func() {
			pktinfo := nflogReader.ConvertNflogPkt(rules.RuleDirIngress, ingressPktAllow[ingressPktAllowNflogTuple])
			c.applyPacketInfo(pktinfo)
			t := tuple.New(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)

			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			// Flag the data as reported, swap local endpoints from mock data and send in packetinfo entry again.
			data := c.epStats[*t]
			data.Reported = true
			oldSrc := data.SrcEp
			oldDest := data.DstEp

			lm.SetMockData(epMapSwapLocal, nil, nil, nil)
			c.applyPacketInfo(pktinfo)

			// This is a reported flow but we are going through packet processing still. It should be expired and
			// the endpoints updated.
			Consistently(c.epStats, "500ms", "100ms").Should(HaveKey(*t))
			Expect(data.Reported).To(BeFalse())
			Expect(data.SrcEp).To(Equal(oldSrc))
			Expect(data.DstEp).NotTo(Equal(oldDest))
		})
		It("should handle destination changing on next packetinfo update for unreported flow", func() {
			pktinfo := nflogReader.ConvertNflogPkt(rules.RuleDirIngress, ingressPktAllow[ingressPktAllowNflogTuple])
			c.applyPacketInfo(pktinfo)
			t := tuple.New(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)

			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			// Data is not reported, swap local endpoints from mock data and send in CT entry again.
			data := c.epStats[*t]
			oldSrc := data.SrcEp
			oldDest := data.DstEp

			lm.SetMockData(epMapSwapLocal, nil, nil, nil)
			c.applyPacketInfo(pktinfo)

			// This is an unreported flow, and is a conntrack update. We can update the endpoint.
			Consistently(c.epStats, "500ms", "100ms").Should(HaveKey(*t))
			Expect(data.Reported).To(BeFalse())
			Expect(data.SrcEp).To(Equal(oldSrc))
			Expect(data.DstEp).NotTo(Equal(oldDest))
		})
	})
	Describe("Test local source", func() {
		It("should create a single entry with outbound direction", func() {
			t := tuple.New(localIp1, remoteIp1, proto_tcp, srcPort, dstPort)

			// will call handlerInfo from c.Start() in BeforeEach
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(outCtEntry, 0)}

			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))
			data := c.epStats[*t]

			Expect(data.ConntrackPacketsCounter()).Should(Equal(*counter.New(outCtEntry.OriginalCounters.Packets)))
			Expect(data.ConntrackPacketsCounterReverse()).Should(Equal(*counter.New(outCtEntry.ReplyCounters.Packets)))
			Expect(data.ConntrackBytesCounter()).Should(Equal(*counter.New(outCtEntry.OriginalCounters.Bytes)))
			Expect(data.ConntrackBytesCounterReverse()).Should(Equal(*counter.New(outCtEntry.ReplyCounters.Bytes)))

			// Not SNAT'd so natOutgoingPort should not be set.
			Expect(data.NatOutgoingPort).Should(Equal(0))
		})
		It("should create a single entry with outbound direction for SNAT'd packet with nat outgoing port set", func() {
			t := tuple.New(localIp1, remoteIp1, proto_tcp, srcPort, dstPort)

			// will call handlerInfo from c.Start() in BeforeEach
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(outCtEntryWithSNAT, 0)}

			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))
			data := c.epStats[*t]

			Expect(data.NatOutgoingPort).Should(Equal(nodeSrcPort))
		})
		It("should create a single entry with outbound direction for SNAT'd packet sent to self without nat outgoing port set", func() {
			t := tuple.New(localIp1, localIp1, proto_tcp, srcPort, srcPort2)

			// will call handlerInfo from c.Start() in BeforeEach
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(outCtEntrySNATToServiceToSelf, 0)}

			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))
			data := c.epStats[*t]

			Expect(data.NatOutgoingPort).Should(Equal(0))
		})
		It("should handle source becoming non-local by removing entry on next conntrack update for reported flow", func() {
			t := tuple.New(localIp1, remoteIp1, proto_tcp, srcPort, dstPort)
			// will call handlerInfo from c.Start() in BeforeEach
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(outCtEntry, 0)}

			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			// Flag the data as reported, remove endpoints from mock data and send in CT entry again.
			data := c.epStats[*t]
			data.Reported = true
			lm.SetMockData(epMapDelete, nil, nil, nil)
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(outCtEntry, 0)}

			// This is a reported flow, and is a conntrack update - this should not impact the stored data at all.
			Consistently(c.epStats, "500ms", "100ms").Should(HaveKey(*t))
		})
		It("should handle source becoming non-local by removing entry on next conntrack update for unreported flow", func() {
			t := tuple.New(localIp1, remoteIp1, proto_tcp, srcPort, dstPort)
			// will call handlerInfo from c.Start() in BeforeEach
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(outCtEntry, 0)}

			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			// Data is not reported. Remove endpoints from mock data and send in CT entry again.
			lm.SetMockData(epMapDelete, nil, nil, nil)
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(outCtEntry, 0)}

			// This is an unreported flow, and is a conntrack update. We can update the endpoint, but we never downgrade
			// to having no endpoint (since we handle the situation where endpoint is deleted before we gather all
			// logs).
			Consistently(c.epStats, "500ms", "100ms").Should(HaveKey(*t))
		})
		It("should handle source changing on next conntrack update for reported flow", func() {
			t := tuple.New(localIp1, remoteIp1, proto_tcp, srcPort, dstPort)
			// will call handlerInfo from c.Start() in BeforeEach
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(outCtEntry, 0)}

			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			// Flag the data as reported, swap local endpoints from mock data and send in CT entry again.
			data := c.epStats[*t]
			data.Reported = true
			oldSrc := data.SrcEp
			oldDest := data.DstEp

			lm.SetMockData(epMapSwapLocal, nil, nil, nil)
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(outCtEntry, 0)}

			// This is a reported flow, and is a conntrack update - this should not impact the stored data at all since
			// the endpoint should not be changing for a constant connection.
			Consistently(c.epStats, "500ms", "100ms").Should(HaveKey(*t))
			Expect(data.SrcEp).To(Equal(oldSrc))
			Expect(data.DstEp).To(Equal(oldDest))
		})
		It("should handle source changing on next conntrack update for unreported flow", func() {
			t := tuple.New(localIp1, remoteIp1, proto_tcp, srcPort, dstPort)
			// will call handlerInfo from c.Start() in BeforeEach
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(outCtEntry, 0)}

			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			// Data is not reported. swap local endpoints from mock data and send in packetinfo entry again.
			data := c.epStats[*t]
			oldSrc := data.SrcEp
			oldDest := data.DstEp

			lm.SetMockData(epMapSwapLocal, nil, nil, nil)
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(outCtEntry, 0)}

			// This is an unreported flow, and is a conntrack update. We can update the endpoint.
			Consistently(c.epStats, "500ms", "100ms").Should(HaveKey(*t))
			Expect(data.SrcEp).NotTo(Equal(oldSrc))
			Expect(data.DstEp).To(Equal(oldDest))
		})
		It("should handle source becoming non-local by removing entry on next packetinfo update for reported flow", func() {
			pktinfo := nflogReader.ConvertNflogPkt(rules.RuleDirEgress, egressPktAllow[egressPktAllowNflogTuple])
			c.applyPacketInfo(pktinfo)
			t := tuple.New(localIp1, remoteIp1, proto_udp, srcPort, dstPort)

			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			// Flag the data as reported, remove endpoints from mock data and send in packetinfo entry again.
			data := c.epStats[*t]
			data.Reported = true
			lm.SetMockData(epMapDelete, nil, nil, nil)
			c.applyPacketInfo(pktinfo)

			// This is a reported flow but we are going through packet processing still. It should be expired and
			// removed.
			Eventually(c.epStats, "500ms", "100ms").ShouldNot(HaveKey(*t))
			Expect(data.Reported).To(BeFalse())
		})
		It("should handle source becoming non-local by removing entry on next packetinfo update for unreported flow", func() {
			pktinfo := nflogReader.ConvertNflogPkt(rules.RuleDirEgress, egressPktAllow[egressPktAllowNflogTuple])
			c.applyPacketInfo(pktinfo)
			t := tuple.New(localIp1, remoteIp1, proto_udp, srcPort, dstPort)

			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			// Data is not reported. Remove endpoints from mock data and send in packetinfo entry again.
			data := c.epStats[*t]
			oldSrc := data.SrcEp
			oldDest := data.DstEp
			lm.SetMockData(epMapDelete, nil, nil, nil)
			c.applyPacketInfo(pktinfo)

			// This is an unreported flow but we are going through packet processing still. However, since the endpoint
			// data has been removed assume it has just been deleted and don't downgrade our endpoint data.
			Consistently(c.epStats, "500ms", "100ms").Should(HaveKey(*t))
			Expect(data.Reported).To(BeFalse())
			Expect(data.SrcEp).To(Equal(oldSrc))
			Expect(data.DstEp).To(Equal(oldDest))
		})
		It("should handle source changing on next packetinfo update for reported flow", func() {
			pktinfo := nflogReader.ConvertNflogPkt(rules.RuleDirEgress, egressPktAllow[egressPktAllowNflogTuple])
			c.applyPacketInfo(pktinfo)
			t := tuple.New(localIp1, remoteIp1, proto_udp, srcPort, dstPort)

			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			// Flag the data as reported, swap local endpoints from mock data and send in packetinfo entry again.
			data := c.epStats[*t]
			data.Reported = true
			oldSrc := data.SrcEp
			oldDest := data.DstEp

			lm.SetMockData(epMapSwapLocal, nil, nil, nil)
			c.applyPacketInfo(pktinfo)

			// This is a reported flow but we are going through packet processing still. It should be expired and
			// the endpoints updated.
			Consistently(c.epStats, "500ms", "100ms").Should(HaveKey(*t))
			Expect(data.Reported).To(BeFalse())
			Expect(data.SrcEp).NotTo(Equal(oldSrc))
			Expect(data.DstEp).To(Equal(oldDest))
		})
		It("should handle source changing on next packetinfo update for unreported flow", func() {
			pktinfo := nflogReader.ConvertNflogPkt(rules.RuleDirEgress, egressPktAllow[egressPktAllowNflogTuple])
			c.applyPacketInfo(pktinfo)
			t := tuple.New(localIp1, remoteIp1, proto_udp, srcPort, dstPort)

			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			// Data is not reported, swap local endpoints from mock data and send in CT entry again.
			data := c.epStats[*t]
			oldSrc := data.SrcEp
			oldDest := data.DstEp

			lm.SetMockData(epMapSwapLocal, nil, nil, nil)
			c.applyPacketInfo(pktinfo)

			// This is an unreported flow, and is a conntrack update. We can update the endpoint.
			Consistently(c.epStats, "500ms", "100ms").Should(HaveKey(*t))
			Expect(data.Reported).To(BeFalse())
			Expect(data.SrcEp).NotTo(Equal(oldSrc))
			Expect(data.DstEp).To(Equal(oldDest))
		})
	})
	Describe("Test local source to local destination", func() {
		It("should create a single entry with 'local' direction", func() {
			t1 := tuple.New(localIp1, localIp2, proto_tcp, srcPort, dstPort)

			// will call handlerInfo from c.Start() in BeforeEach
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(localCtEntry, 0)}

			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t1))

			data := c.epStats[*t1]
			Expect(data.ConntrackPacketsCounter()).Should(Equal(*counter.New(localCtEntry.OriginalCounters.Packets)))
			Expect(data.ConntrackPacketsCounterReverse()).Should(Equal(*counter.New(localCtEntry.ReplyCounters.Packets)))
			Expect(data.ConntrackBytesCounter()).Should(Equal(*counter.New(localCtEntry.OriginalCounters.Bytes)))
			Expect(data.ConntrackBytesCounterReverse()).Should(Equal(*counter.New(localCtEntry.ReplyCounters.Bytes)))
		})
	})
	Describe("Test local destination with DNAT", func() {
		It("should create a single entry with inbound connection direction and with correct tuple extracted", func() {
			t := tuple.New(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)

			// will call handlerInfo from c.Start() in BeforeEach
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(inCtEntryWithDNAT, 0)}

			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			data := c.epStats[*t]
			Expect(data.ConntrackPacketsCounter()).Should(Equal(*counter.New(inCtEntryWithDNAT.OriginalCounters.Packets)))
			Expect(data.ConntrackPacketsCounterReverse()).Should(Equal(*counter.New(inCtEntryWithDNAT.ReplyCounters.Packets)))
			Expect(data.ConntrackBytesCounter()).Should(Equal(*counter.New(inCtEntryWithDNAT.OriginalCounters.Bytes)))
			Expect(data.ConntrackBytesCounterReverse()).Should(Equal(*counter.New(inCtEntryWithDNAT.ReplyCounters.Bytes)))
		})
	})
	Describe("Test local source to local destination with DNAT", func() {
		It("should create a single entry with 'local' connection direction and with correct tuple extracted", func() {
			t1 := tuple.New(localIp1, localIp2, proto_tcp, srcPort, dstPort)
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(localCtEntryWithDNAT, 0)}
			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey((Equal(*t1))))
			data := c.epStats[*t1]
			Expect(data.ConntrackPacketsCounter()).Should(Equal(*counter.New(localCtEntryWithDNAT.OriginalCounters.Packets)))
			Expect(data.ConntrackPacketsCounterReverse()).Should(Equal(*counter.New(localCtEntryWithDNAT.ReplyCounters.Packets)))
			Expect(data.ConntrackBytesCounter()).Should(Equal(*counter.New(localCtEntryWithDNAT.OriginalCounters.Bytes)))
			Expect(data.ConntrackBytesCounterReverse()).Should(Equal(*counter.New(localCtEntryWithDNAT.ReplyCounters.Bytes)))
		})
	})
	Describe("Test conntrack TCP Protoinfo State", func() {
		It("Handle TCP conntrack entries with TCP state TIME_WAIT after NFLOGs gathered", func() {
			By("handling a conntrack update to start tracking stats for tuple")
			t := tuple.New(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(inCtEntry, 0)}
			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))
			data := c.epStats[*t]
			Expect(data.ConntrackPacketsCounter()).Should(Equal(*counter.New(inCtEntry.OriginalCounters.Packets)))
			Expect(data.ConntrackPacketsCounterReverse()).Should(Equal(*counter.New(inCtEntry.ReplyCounters.Packets)))
			Expect(data.ConntrackBytesCounter()).Should(Equal(*counter.New(inCtEntry.OriginalCounters.Bytes)))
			Expect(data.ConntrackBytesCounterReverse()).Should(Equal(*counter.New(inCtEntry.ReplyCounters.Bytes)))

			By("handling a conntrack update with updated counters")
			inCtEntryUpdatedCounters := inCtEntry
			inCtEntryUpdatedCounters.OriginalCounters.Packets = inCtEntry.OriginalCounters.Packets + 1
			inCtEntryUpdatedCounters.OriginalCounters.Bytes = inCtEntry.OriginalCounters.Bytes + 10
			inCtEntryUpdatedCounters.ReplyCounters.Packets = inCtEntry.ReplyCounters.Packets + 2
			inCtEntryUpdatedCounters.ReplyCounters.Bytes = inCtEntry.ReplyCounters.Bytes + 50
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(inCtEntryUpdatedCounters, 0)}
			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))
			// know update is complete
			Eventually(func() counter.Counter {
				return c.epStats[*t].ConntrackPacketsCounter()
			}, "500ms", "100ms").Should(Equal(*counter.New(inCtEntryUpdatedCounters.OriginalCounters.Packets)))

			data = c.epStats[*t]
			Expect(data.ConntrackPacketsCounterReverse()).Should(Equal(*counter.New(inCtEntryUpdatedCounters.ReplyCounters.Packets)))
			Expect(data.ConntrackBytesCounter()).Should(Equal(*counter.New(inCtEntryUpdatedCounters.OriginalCounters.Bytes)))
			Expect(data.ConntrackBytesCounterReverse()).Should(Equal(*counter.New(inCtEntryUpdatedCounters.ReplyCounters.Bytes)))

			By("handling a conntrack update with TCP CLOSE_WAIT")
			inCtEntryStateCloseWait := inCtEntryUpdatedCounters
			inCtEntryStateCloseWait.ProtoInfo.State = nfnl.TCP_CONNTRACK_CLOSE_WAIT
			inCtEntryStateCloseWait.ReplyCounters.Packets = inCtEntryUpdatedCounters.ReplyCounters.Packets + 1
			inCtEntryStateCloseWait.ReplyCounters.Bytes = inCtEntryUpdatedCounters.ReplyCounters.Bytes + 10
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(inCtEntryStateCloseWait, 0)}
			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))
			// know update is complete
			Eventually(func() counter.Counter {
				return c.epStats[*t].ConntrackPacketsCounterReverse()
			}, "500ms", "100ms").Should(Equal(*counter.New(inCtEntryStateCloseWait.ReplyCounters.Packets)))

			data = c.epStats[*t]
			Expect(data.ConntrackPacketsCounter()).Should(Equal(*counter.New(inCtEntryStateCloseWait.OriginalCounters.Packets)))
			Expect(data.ConntrackBytesCounter()).Should(Equal(*counter.New(inCtEntryStateCloseWait.OriginalCounters.Bytes)))
			Expect(data.ConntrackBytesCounterReverse()).Should(Equal(*counter.New(inCtEntryStateCloseWait.ReplyCounters.Bytes)))

			By("handling an nflog update for destination matching on policy - all policy info is now gathered",
				func() {
					pktinfo := nflogReader.ConvertNflogPkt(rules.RuleDirIngress, ingressPktAllow[ingressPktAllowNflogTuple])
					c.applyPacketInfo(pktinfo)
				},
			)

			By("handling a conntrack update with TCP TIME_WAIT")
			inCtEntryStateTimeWait := inCtEntry
			inCtEntryStateTimeWait.ProtoInfo.State = nfnl.TCP_CONNTRACK_TIME_WAIT
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(inCtEntryStateTimeWait, 0)}
			Eventually(c.epStats, "500ms", "100ms").ShouldNot(HaveKey(*t))
		})
		It("Handle TCP conntrack entries with TCP state TIME_WAIT before NFLOGs gathered", func() {
			By("handling a conntrack update to start tracking stats for tuple")
			t := tuple.New(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(inCtEntry, 0)}
			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			// know update is complete
			Eventually(func() counter.Counter {
				return c.epStats[*t].ConntrackPacketsCounter()
			}, "500ms", "100ms").Should(Equal(*counter.New(inCtEntry.OriginalCounters.Packets)))
			data := c.epStats[*t]

			Expect(data.ConntrackPacketsCounterReverse()).Should(Equal(*counter.New(inCtEntry.ReplyCounters.Packets)))
			Expect(data.ConntrackBytesCounter()).Should(Equal(*counter.New(inCtEntry.OriginalCounters.Bytes)))
			Expect(data.ConntrackBytesCounterReverse()).Should(Equal(*counter.New(inCtEntry.ReplyCounters.Bytes)))

			By("handling a conntrack update with updated counters")
			inCtEntryUpdatedCounters := inCtEntry
			inCtEntryUpdatedCounters.OriginalCounters.Packets = inCtEntry.OriginalCounters.Packets + 1
			inCtEntryUpdatedCounters.OriginalCounters.Bytes = inCtEntry.OriginalCounters.Bytes + 10
			inCtEntryUpdatedCounters.ReplyCounters.Packets = inCtEntry.ReplyCounters.Packets + 2
			inCtEntryUpdatedCounters.ReplyCounters.Bytes = inCtEntry.ReplyCounters.Bytes + 50
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(inCtEntryUpdatedCounters, 0)}
			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			// know update is complete
			Eventually(func() counter.Counter {
				return c.epStats[*t].ConntrackPacketsCounter()
			}, "500ms", "100ms").Should(Equal(*counter.New(inCtEntryUpdatedCounters.OriginalCounters.Packets)))
			data = c.epStats[*t]

			Expect(data.ConntrackPacketsCounterReverse()).Should(Equal(*counter.New(inCtEntryUpdatedCounters.ReplyCounters.Packets)))
			Expect(data.ConntrackBytesCounter()).Should(Equal(*counter.New(inCtEntryUpdatedCounters.OriginalCounters.Bytes)))
			Expect(data.ConntrackBytesCounterReverse()).Should(Equal(*counter.New(inCtEntryUpdatedCounters.ReplyCounters.Bytes)))

			By("handling a conntrack update with TCP CLOSE_WAIT")
			inCtEntryStateCloseWait := inCtEntryUpdatedCounters
			inCtEntryStateCloseWait.ProtoInfo.State = nfnl.TCP_CONNTRACK_CLOSE_WAIT
			inCtEntryStateCloseWait.ReplyCounters.Packets = inCtEntryUpdatedCounters.ReplyCounters.Packets + 1
			inCtEntryStateCloseWait.ReplyCounters.Bytes = inCtEntryUpdatedCounters.ReplyCounters.Bytes + 10
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(inCtEntryStateCloseWait, 0)}
			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			// know update is complete
			Eventually(func() counter.Counter {
				return c.epStats[*t].ConntrackPacketsCounterReverse()
			}, "500ms", "100ms").Should(Equal(*counter.New(inCtEntryStateCloseWait.ReplyCounters.Packets)))
			data = c.epStats[*t]
			Expect(data.ConntrackPacketsCounter()).Should(Equal(*counter.New(inCtEntryStateCloseWait.OriginalCounters.Packets)))
			Expect(data.ConntrackBytesCounter()).Should(Equal(*counter.New(inCtEntryStateCloseWait.OriginalCounters.Bytes)))
			Expect(data.ConntrackBytesCounterReverse()).Should(Equal(*counter.New(inCtEntryStateCloseWait.ReplyCounters.Bytes)))

			By("handling a conntrack update with TCP TIME_WAIT")
			inCtEntryStateTimeWait := inCtEntry
			inCtEntryStateTimeWait.ProtoInfo.State = nfnl.TCP_CONNTRACK_TIME_WAIT
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(inCtEntryStateTimeWait, 0)}
			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			By("handling an nflog update for destination matching on policy - all policy info is now gathered",
				func() {
					pktinfo := nflogReader.ConvertNflogPkt(rules.RuleDirIngress, ingressPktAllow[ingressPktAllowNflogTuple])
					c.applyPacketInfo(pktinfo)
				},
			)
			Eventually(c.epStats, "500ms", "100ms").ShouldNot(HaveKey(*t))
		})
	})

	Describe("Test data race", func() {
		It("getDataAndUpdateEndpoints does not cause a data race contention with deleteDataFromEpStats after deleteDataFromEpStats removes it from epstats", func() {
			existingTuple := tuple.New(remoteIp1, localIp1, proto_tcp, srcPort, dstPort)
			testData := c.getDataAndUpdateEndpoints(*existingTuple, false, true)

			newTuple := tuple.New(localIp1, localIp2, proto_tcp, srcPort, dstPort)

			var resultantNewTupleData *Data

			time.AfterFunc(2*time.Second, func() {
				c.deleteDataFromEpStats(testData)
			})

			// ok Get is a little after feedupdate because feedupdate has some preprocesssing
			// before it accesses flowstore
			time.AfterFunc(2*time.Second+10*time.Millisecond, func() {
				resultantNewTupleData = c.getDataAndUpdateEndpoints(*newTuple, false, true)
			})

			time.Sleep(3 * time.Second)

			Expect(c.epStats).ShouldNot(HaveKey(*existingTuple))
			Expect(c.epStats).Should(HaveKey(*newTuple))
			Expect(resultantNewTupleData).ToNot(Equal(nil))
		})
	})

	Describe("Test pre-DNAT handling", func() {
		It("handle pre-DNAT info on conntrack", func() {
			By("handling a conntrack update to start tracking stats for tuple (w/ DNAT)")
			t := tuple.New(localIp1, localIp2, proto_tcp, srcPort, dstPort)
			ciReaderSenderChan <- []clttypes.ConntrackInfo{convertCtEntry(localCtEntryWithDNAT, 0)}
			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			// Flagging as expired will attempt to expire the data when NFLOGs and service info are gathered.
			By("flagging the data as expired")
			data := c.epStats[*t]
			data.Expired = true
			Expect(data.IsDNAT).Should(BeTrue())

			By("handling nflog updates for destination matching on policy - all policy info is now gathered, but no service")
			c.applyPacketInfo(nflogReader.ConvertNflogPkt(rules.RuleDirIngress, localPktIngress[localPktIngressNflogTuple]))
			c.applyPacketInfo(nflogReader.ConvertNflogPkt(rules.RuleDirEgress, localPktEgress[localPktEgressNflogTuple]))
			Eventually(c.epStats, "500ms", "100ms").Should(HaveKey(*t))

			By("creating a matching service for the pre-DNAT cluster IP and port")
			lm.SetMockData(nil, nil, nil, map[model.ResourceKey]*kapiv1.Service{
				{Kind: model.KindKubernetesService, Name: "svc", Namespace: "default"}: {Spec: kapiv1.ServiceSpec{
					Ports: []kapiv1.ServicePort{{
						Name:     "test",
						Protocol: kapiv1.ProtocolTCP,
						Port:     int32(dstPortDNAT),
					}},
					ClusterIP: "192.168.0.2",
					ClusterIPs: []string{
						"192.168.0.2",
					},
				},
				},
			})

			By("handling another nflog update for destination matching on policy - should rematch and expire the entry")
			c.applyPacketInfo(nflogReader.ConvertNflogPkt(rules.RuleDirIngress, localPktIngress[localPktIngressNflogTuple]))
			Expect(c.epStats).ShouldNot(HaveKey(*t))
		})
		It("handle pre-DNAT info on nflog update", func() {
			By("handling egress nflog updates for destination matching on policy - this contains pre-DNAT info")
			t := tuple.New(localIp1, localIp2, proto_tcp, srcPort, dstPort)
			c.applyPacketInfo(nflogReader.ConvertNflogPkt(rules.RuleDirIngress, localPktIngressWithDNAT[localPktIngressWithDNATNflogTuple]))

			// Flagging as expired will attempt to expire the data when NFLOGs and service info are gathered.
			By("flagging the data as expired")
			data := c.epStats[*t]
			data.Expired = true
			Expect(data.IsDNAT).Should(BeTrue())

			By("handling ingree nflog updates for destination matching on policy - all policy info is now gathered, but no service")
			c.applyPacketInfo(nflogReader.ConvertNflogPkt(rules.RuleDirEgress, localPktEgress[localPktEgressNflogTuple]))
			Expect(c.epStats).Should(HaveKey(*t))

			By("creating a matching service for the pre-DNAT cluster IP and port")
			lm.SetMockData(nil, nil, nil, map[model.ResourceKey]*kapiv1.Service{
				{Kind: model.KindKubernetesService, Name: "svc", Namespace: "default"}: {Spec: kapiv1.ServiceSpec{
					Ports: []kapiv1.ServicePort{{
						Name:     "test",
						Protocol: kapiv1.ProtocolTCP,
						Port:     int32(dstPortDNAT),
					}},
					ClusterIP: "192.168.0.2",
					ClusterIPs: []string{
						"192.168.0.2",
					},
				},
				},
			})

			By("handling another nflog update for destination matching on policy - should rematch and expire the entry")
			c.applyPacketInfo(nflogReader.ConvertNflogPkt(rules.RuleDirIngress, localPktIngress[localPktIngressNflogTuple]))
			Expect(c.epStats).ShouldNot(HaveKey(*t))
		})
	})
})

func policyIDStrToRuleIDParts(r *calc.RuleID) [64]byte {
	var (
		name  string
		byt64 [64]byte
	)

	if r.Namespace != "" {
		if strings.HasPrefix(r.Name, "knp.default.") {
			name = fmt.Sprintf("%s/%s", r.Namespace, r.Name)
		} else {
			name = fmt.Sprintf("%s/%s.%s", r.Namespace, r.Tier, r.Name)
		}
	} else {
		name = fmt.Sprintf("%s.%s", r.Tier, r.Name)
	}

	prefix := rules.CalculateNFLOGPrefixStr(r.Action, rules.RuleOwnerTypePolicy, r.Direction, r.Index, name)
	copy(byt64[:], []byte(prefix))
	return byt64
}

var _ = Describe("Reporting Metrics", func() {
	var c *collector
	var nflogReader *NFLogReader
	var mockReporter *mockReporter
	var lm *calc.LookupsCache

	const (
		ageTimeout            = time.Duration(3) * time.Second
		reportingDelay        = time.Duration(2) * time.Second
		exportingInterval     = time.Duration(1) * time.Second
		flowLogsFlushInterval = time.Duration(1) * time.Second
	)
	conf := &Config{
		AgeTimeout:            ageTimeout,
		InitialReportingDelay: reportingDelay,
		ExportingInterval:     exportingInterval,
		FlowLogsFlushInterval: flowLogsFlushInterval,
		DisplayDebugTraceLogs: true,
	}
	BeforeEach(func() {
		epMap := map[[16]byte]calc.EndpointData{
			localIp1:  localEd1,
			localIp2:  localEd2,
			remoteIp1: remoteEd1,
		}

		nflogMap := map[[64]byte]*calc.RuleID{}

		for _, rid := range []*calc.RuleID{defTierPolicy1AllowEgressRuleID, defTierPolicy1AllowIngressRuleID, defTierPolicy2DenyIngressRuleID, defTierPolicy2DenyEgressRuleID} {
			nflogMap[policyIDStrToRuleIDParts(rid)] = rid
		}

		lm = newMockLookupsCache(epMap, nflogMap, nil, nil)
		mockReporter = newMockReporter()
		nflogReader = NewNFLogReader(lm, 0, 0, 0, false)
		Expect(nflogReader.Start()).NotTo(HaveOccurred())
		c = newCollector(lm, conf).(*collector)
		c.RegisterMetricsReporter(mockReporter)
		c.SetPacketInfoReader(nflogReader)
		c.SetConntrackInfoReader(dummyConntrackInfoReader{})
	})
	AfterEach(func() {
		nflogReader.Stop()
	})
	Context("Without process info enabled", func() {
		BeforeEach(func() {
			go func() {
				Expect(c.Start()).NotTo(HaveOccurred())
			}()
		})
		Describe("Report Denied Packets", func() {
			BeforeEach(func() {
				nflogReader.IngressC <- ingressPktDeny
			})
			Context("reporting tick", func() {
				It("should receive metric", func() {
					tmu := testMetricUpdate{
						updateType:   metric.UpdateTypeReport,
						tpl:          *ingressPktDenyTuple,
						srcEp:        remoteEd1,
						dstEp:        localEd1,
						ruleIDs:      []*calc.RuleID{defTierPolicy2DenyIngressRuleID},
						isConnection: false,
					}
					Eventually(mockReporter.reportChan, reportingDelay*2).Should(Receive(Equal(tmu)))
				})
			})
		})
		Describe("Report Allowed Packets (ingress)", func() {
			BeforeEach(func() {
				nflogReader.IngressC <- ingressPktAllow
			})
			Context("reporting tick", func() {
				It("should receive metric", func() {
					tmu := testMetricUpdate{
						updateType:   metric.UpdateTypeReport,
						tpl:          *ingressPktAllowTuple,
						srcEp:        remoteEd1,
						dstEp:        localEd1,
						ruleIDs:      []*calc.RuleID{defTierPolicy1AllowIngressRuleID},
						isConnection: false,
					}
					Eventually(mockReporter.reportChan, reportingDelay*2).Should(Receive(Equal(tmu)))
				})
			})
		})
		Describe("Report Packets that switch from deny to allow", func() {
			BeforeEach(func() {
				nflogReader.IngressC <- ingressPktDeny
				time.Sleep(time.Duration(500) * time.Millisecond)
				nflogReader.IngressC <- ingressPktAllow
			})
			Context("reporting tick", func() {
				It("should receive metric", func() {
					tmu := testMetricUpdate{
						updateType:   metric.UpdateTypeReport,
						tpl:          *ingressPktAllowTuple,
						srcEp:        remoteEd1,
						dstEp:        localEd1,
						ruleIDs:      []*calc.RuleID{defTierPolicy1AllowIngressRuleID},
						isConnection: false,
					}
					Eventually(mockReporter.reportChan, reportingDelay*2).Should(Receive(Equal(tmu)))
				})
			})
		})
		Describe("Report Allowed Packets (egress)", func() {
			BeforeEach(func() {
				nflogReader.EgressC <- egressPktAllow
			})
			Context("reporting tick", func() {
				It("should receive metric", func() {
					tmu := testMetricUpdate{
						updateType:   metric.UpdateTypeReport,
						tpl:          *egressPktAllowTuple,
						srcEp:        localEd1,
						dstEp:        remoteEd1,
						ruleIDs:      []*calc.RuleID{defTierPolicy1AllowEgressRuleID},
						isConnection: false,
					}
					Eventually(mockReporter.reportChan, reportingDelay*2).Should(Receive(Equal(tmu)))
				})
			})
		})
	})
})

func newMockLookupsCache(
	em map[[16]byte]calc.EndpointData,
	nm map[[64]byte]*calc.RuleID,
	ns map[model.NetworkSetKey]*model.NetworkSet,
	svcs map[model.ResourceKey]*kapiv1.Service,
) *calc.LookupsCache {
	l := calc.NewLookupsCache()
	l.SetMockData(em, nm, ns, svcs)
	return l
}

// Define a separate metric type that doesn't include the actual stats.  We use this
// for simpler comparisons.
type testMetricUpdate struct {
	updateType metric.UpdateType

	// Tuple key
	tpl tuple.Tuple

	// Endpoint information.
	srcEp calc.EndpointData
	dstEp calc.EndpointData

	// Rules identification
	ruleIDs []*calc.RuleID

	// Sometimes we may need to send updates without having all the rules
	// in place. This field will help aggregators determine if they need
	// to handle this update or not. Typically this is used when we receive
	// HTTP Data updates after the connection itself has closed.
	unknownRuleID *calc.RuleID

	// isConnection is true if this update is from an active connection (i.e. a conntrack
	// update compared to an NFLOG update).
	isConnection bool
}

// Create a mockReporter that acts as a pass-thru of the updates.
type mockReporter struct {
	reportChan chan testMetricUpdate
}

func newMockReporter() *mockReporter {
	return &mockReporter{
		reportChan: make(chan testMetricUpdate),
	}
}

func (mr *mockReporter) Start() error {
	return nil
}

func (mr *mockReporter) Report(u any) error {
	mu, ok := u.(metric.Update)
	if !ok {
		return fmt.Errorf("invalid metric update")
	}
	mr.reportChan <- testMetricUpdate{
		updateType:    mu.UpdateType,
		tpl:           mu.Tuple,
		srcEp:         mu.SrcEp,
		dstEp:         mu.DstEp,
		ruleIDs:       mu.RuleIDs,
		unknownRuleID: mu.UnknownRuleID,
		isConnection:  mu.IsConnection,
	}
	return nil
}

func BenchmarkNflogPktToStat(b *testing.B) {
	epMap := map[[16]byte]calc.EndpointData{
		localIp1:  localEd1,
		localIp2:  localEd2,
		remoteIp1: remoteEd1,
	}

	nflogMap := map[[64]byte]*calc.RuleID{}

	for _, rid := range []*calc.RuleID{defTierPolicy1AllowEgressRuleID, defTierPolicy1AllowIngressRuleID, defTierPolicy2DenyIngressRuleID, defTierPolicy2DenyEgressRuleID} {
		nflogMap[policyIDStrToRuleIDParts(rid)] = rid
	}

	conf := &Config{
		AgeTimeout:            time.Duration(10) * time.Second,
		InitialReportingDelay: time.Duration(5) * time.Second,
		ExportingInterval:     time.Duration(1) * time.Second,
		FlowLogsFlushInterval: time.Duration(100) * time.Second,
		DisplayDebugTraceLogs: true,
	}
	lm := newMockLookupsCache(epMap, nflogMap, nil, nil)
	nflogReader := NewNFLogReader(lm, 0, 0, 0, false)
	c := newCollector(lm, conf).(*collector)
	c.SetPacketInfoReader(nflogReader)
	c.SetConntrackInfoReader(dummyConntrackInfoReader{})
	b.ResetTimer()
	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		pktinfo := nflogReader.ConvertNflogPkt(rules.RuleDirIngress, ingressPktAllow[ingressPktAllowNflogTuple])
		c.applyPacketInfo(pktinfo)
	}
}

func BenchmarkApplyStatUpdate(b *testing.B) {
	epMap := map[[16]byte]calc.EndpointData{
		localIp1:  localEd1,
		localIp2:  localEd2,
		remoteIp1: remoteEd1,
	}

	nflogMap := map[[64]byte]*calc.RuleID{}
	for _, rid := range []*calc.RuleID{defTierPolicy1AllowEgressRuleID, defTierPolicy1AllowIngressRuleID, defTierPolicy2DenyIngressRuleID, defTierPolicy2DenyEgressRuleID} {
		nflogMap[policyIDStrToRuleIDParts(rid)] = rid
	}

	conf := &Config{
		AgeTimeout:            time.Duration(10) * time.Second,
		InitialReportingDelay: time.Duration(5) * time.Second,
		ExportingInterval:     time.Duration(1) * time.Second,
		FlowLogsFlushInterval: time.Duration(100) * time.Second,
		DisplayDebugTraceLogs: true,
	}
	lm := newMockLookupsCache(epMap, nflogMap, nil, nil)
	nflogReader := NewNFLogReader(lm, 0, 0, 0, false)
	c := newCollector(lm, conf).(*collector)
	c.SetPacketInfoReader(nflogReader)
	c.SetConntrackInfoReader(dummyConntrackInfoReader{})
	var tuples []tuple.Tuple
	MaxSrcPort := 1000
	MaxDstPort := 1000
	for sp := 1; sp < MaxSrcPort; sp++ {
		for dp := 1; dp < MaxDstPort; dp++ {
			t := tuple.New(localIp1, localIp2, proto_tcp, sp, dp)
			tuples = append(tuples, *t)
		}
	}
	var rids []*calc.RuleID
	MaxEntries := 10000
	for i := 0; i < MaxEntries; i++ {
		rid := defTierPolicy1AllowIngressRuleID
		rids = append(rids, rid)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		for i := 0; i < MaxEntries; i++ {
			data := NewData(tuples[i], localEd1, remoteEd1)
			c.applyNflogStatUpdate(data, rids[i], 0, 1, 2)
		}
	}
}

type dummyConntrackInfoReader struct {
	MockSenderChannel chan []clttypes.ConntrackInfo
}

func (d dummyConntrackInfoReader) Start() error { return nil }
func (d dummyConntrackInfoReader) ConntrackInfoChan() <-chan []clttypes.ConntrackInfo {
	return d.MockSenderChannel
}

func TestLoopDataplaneInfoUpdates(t *testing.T) {
	RegisterTestingT(t)

	// Setup helper function to initialize the collector and channel, and register cleanup.
	setup := func(t *testing.T) (*collector, chan *proto.ToDataplane) {
		dpInfoChan := make(chan *proto.ToDataplane, 10)
		c := &collector{
			policyStoreManager: policystore.NewPolicyStoreManager(),
		}
		// Register cleanup to be automatically called at the end of each test
		t.Cleanup(func() {
			close(dpInfoChan)
		})

		// Start the loop in a goroutine
		go c.loopProcessingDataplaneInfoUpdates(dpInfoChan)

		return c, dpInfoChan
	}

	insync := func(dpInfoChan chan *proto.ToDataplane) {
		// Ensure that the test channel is closed at the end of each test
		dpInfo := proto.ToDataplane{
			Payload: &proto.ToDataplane_InSync{
				InSync: &proto.InSync{},
			},
		}
		dpInfoChan <- &dpInfo
	}

	t.Run("should process dataplane info updates and update the policy store", func(t *testing.T) {
		c, dpInfoChan := setup(t)

		id := proto.WorkloadEndpointID{
			OrchestratorId: "test-orchestrator",
			WorkloadId:     "test-workload",
			EndpointId:     "test-endpoint",
		}
		dpInfo := proto.ToDataplane{
			Payload: &proto.ToDataplane_WorkloadEndpointUpdate{
				WorkloadEndpointUpdate: &proto.WorkloadEndpointUpdate{
					Id: &id,
					Endpoint: &proto.WorkloadEndpoint{
						Name: "test-endpoint",
					},
				},
			},
		}
		dpInfoChan <- &dpInfo
		insync(dpInfoChan)

		Eventually(func() bool {
			validation := false
			c.policyStoreManager.DoWithReadLock(func(store *policystore.PolicyStore) {
				validation = len(store.Endpoints) == 1 &&
					store.Endpoints[types.ProtoToWorkloadEndpointID(&id)].Name == "test-endpoint"
			})
			return validation
		}, time.Duration(time.Second*5), time.Millisecond*1000).Should(BeTrue())
	})

	t.Run("should handle multiple dataplane info updates", func(t *testing.T) {
		c, dpInfoChan := setup(t)

		id1 := proto.WorkloadEndpointID{
			OrchestratorId: "test-orchestrator1",
			WorkloadId:     "test-workload1",
			EndpointId:     "test-endpoint1",
		}
		id2 := proto.WorkloadEndpointID{
			OrchestratorId: "test-orchestrator2",
			WorkloadId:     "test-workload2",
			EndpointId:     "test-endpoint2",
		}

		dpInfo1 := &proto.ToDataplane{
			Payload: &proto.ToDataplane_WorkloadEndpointUpdate{
				WorkloadEndpointUpdate: &proto.WorkloadEndpointUpdate{
					Id: &id1,
					Endpoint: &proto.WorkloadEndpoint{
						Name: "test-endpoint1",
					},
				},
			},
		}
		dpInfo2 := &proto.ToDataplane{
			Payload: &proto.ToDataplane_WorkloadEndpointUpdate{
				WorkloadEndpointUpdate: &proto.WorkloadEndpointUpdate{
					Id: &id2,
					Endpoint: &proto.WorkloadEndpoint{
						Name: "test-endpoint2",
					},
				},
			},
		}
		dpInfoChan <- dpInfo1
		dpInfoChan <- dpInfo2
		insync(dpInfoChan)

		Eventually(func() bool {
			validation := false
			c.policyStoreManager.DoWithReadLock(func(store *policystore.PolicyStore) {
				validation = len(store.Endpoints) == 2 &&
					store.Endpoints[types.ProtoToWorkloadEndpointID(&id1)].Name == "test-endpoint1" &&
					store.Endpoints[types.ProtoToWorkloadEndpointID(&id2)].Name == "test-endpoint2"
			})
			return validation
		}, time.Duration(time.Second*5), time.Millisecond*1000).Should(BeTrue())

	})

	t.Run("should not panic when the channel is closed", func(t *testing.T) {
		dpInfoChan := make(chan *proto.ToDataplane, 10)
		c := &collector{
			policyStoreManager: policystore.NewPolicyStoreManager(),
		}

		close(dpInfoChan)
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("The code panicked, but it should not have: %v", r)
			}
		}()
		// The loop should exit without panicking
		c.loopProcessingDataplaneInfoUpdates(dpInfoChan)
	})
}

func TestRunPendingRuleTraceEvaluation(t *testing.T) {
	RegisterTestingT(t)

	data1 := &Data{
		Tuple: tuple.Tuple{
			Src:   utils.IpStrTo16Byte("192.168.1.1"),
			Dst:   utils.IpStrTo16Byte("10.0.0.1"),
			Proto: proto_tcp,
			L4Src: 12345,
			L4Dst: 80,
		},
		SrcEp: &calc.LocalEndpointData{
			CommonEndpointData: calc.CalculateCommonEndpointData(
				model.WorkloadEndpointKey{
					OrchestratorID: "k8s",
					WorkloadID:     "default.workload1",
					EndpointID:     "eth0",
				},
				&model.WorkloadEndpoint{},
			),
		},
		DstEp: &calc.LocalEndpointData{
			CommonEndpointData: calc.CalculateCommonEndpointData(
				model.WorkloadEndpointKey{
					OrchestratorID: "k8s",
					WorkloadID:     "default.workload2",
					EndpointID:     "eth0",
				},
				&model.WorkloadEndpoint{},
			),
		},
	}

	policyStoreManager := policystore.NewPolicyStoreManager()
	policyStoreManager.DoWithLock(func(ps *policystore.PolicyStore) {
		ps.Endpoints[types.WorkloadEndpointID{
			OrchestratorId: "k8s",
			WorkloadId:     "default.workload1",
			EndpointId:     "eth0",
		}] = &proto.WorkloadEndpoint{
			State: "active",
			Name:  "eth0",
			Tiers: []*proto.TierInfo{
				{
					Name:           "default",
					EgressPolicies: []string{"policy1"},
				},
			},
		}
		ps.Endpoints[types.WorkloadEndpointID{
			OrchestratorId: "k8s",
			WorkloadId:     "default.workload2",
			EndpointId:     "eth0",
		}] = &proto.WorkloadEndpoint{
			State: "active",
			Name:  "eth0",
			Tiers: []*proto.TierInfo{
				{
					Name:            "default",
					IngressPolicies: []string{"policy1"},
				},
			},
		}

		ps.PolicyByID[types.PolicyID{
			Tier: "default",
			Name: "policy1",
		}] = &proto.Policy{
			InboundRules: []*proto.Rule{
				{
					Action: "allow",
				},
			},
			OutboundRules: []*proto.Rule{
				{
					Action: "allow",
				},
			},
		}

		ps.PolicyByID[types.PolicyID{
			Tier: "tier1",
			Name: "policy11",
		}] = &proto.Policy{
			InboundRules: []*proto.Rule{
				{
					Action: "allow",
				},
			},
			OutboundRules: []*proto.Rule{
				{
					Action: "deny",
				},
			},
		}
	})
	policyStoreManager.OnInSync()
	c := &collector{
		epStats:               make(map[tuple.Tuple]*Data),
		policyStoreManager:    policyStoreManager,
		displayDebugTraceLogs: false,
	}

	c.epStats[data1.Tuple] = data1

	// Add a second data entry
	data2 := &Data{
		Tuple: tuple.Tuple{
			Src:   utils.IpStrTo16Byte("192.168.1.2"),
			Dst:   utils.IpStrTo16Byte("10.0.0.2"),
			Proto: proto_tcp,
			L4Src: 12346,
			L4Dst: 81,
		},
		SrcEp: &calc.LocalEndpointData{
			CommonEndpointData: calc.CalculateCommonEndpointData(
				model.WorkloadEndpointKey{
					OrchestratorID: "k8s",
					WorkloadID:     "default.workload3",
					EndpointID:     "eth1",
				},
				&model.WorkloadEndpoint{},
			),
		},
		DstEp: &calc.LocalEndpointData{
			CommonEndpointData: calc.CalculateCommonEndpointData(
				model.WorkloadEndpointKey{
					OrchestratorID: "k8s",
					WorkloadID:     "default.workload4",
					EndpointID:     "eth1",
				},
				&model.WorkloadEndpoint{},
			),
		},
	}
	c.policyStoreManager.DoWithLock(func(ps *policystore.PolicyStore) {
		ps.Endpoints[types.WorkloadEndpointID{
			OrchestratorId: "k8s",
			WorkloadId:     "default.workload3",
			EndpointId:     "eth1",
		}] = &proto.WorkloadEndpoint{
			State: "active",
			Name:  "eth1",
			Tiers: []*proto.TierInfo{
				{
					Name:           "tier1",
					EgressPolicies: []string{"policy11"},
				},
			},
		}
		ps.Endpoints[types.WorkloadEndpointID{
			OrchestratorId: "k8s",
			WorkloadId:     "default.workload4",
			EndpointId:     "eth1",
		}] = &proto.WorkloadEndpoint{
			State: "active",
			Name:  "eth1",
			Tiers: []*proto.TierInfo{
				{
					Name:            "tier1",
					IngressPolicies: []string{"policy11"},
				},
			},
		}
	})
	c.epStats[data2.Tuple] = data2

	t.Run("updatePendingRuleTraces", func(t *testing.T) {
		// Update pending rule traces
		c.updatePendingRuleTraces()

		for _, ruleID := range []struct {
			iteration      int
			pendingRuleIDs []*calc.RuleID
			expectedRuleID *calc.RuleID
		}{
			{0, data1.IngressPendingRuleIDs, defTierPolicy1AllowIngressRuleID},
			{1, data1.EgressPendingRuleIDs, defTierPolicy1AllowEgressRuleID},
			{2, data2.IngressPendingRuleIDs, tier1TierPolicy1AllowIngressRuleID},
			{3, data2.EgressPendingRuleIDs, tier1TierPolicy1DenyEgressRuleID},
		} {
			Expect(ruleID.pendingRuleIDs).To(HaveLen(1), "Iteration: %s.Expected PendingRuleIDs to be updated")
			Expect(ruleID.pendingRuleIDs[0].Name).To(Equal(ruleID.expectedRuleID.Name), "Iteration: %s.Expected policy name to be: %s", ruleID.iteration, ruleID.expectedRuleID.Name)
			Expect(ruleID.pendingRuleIDs[0].Tier).To(Equal(ruleID.expectedRuleID.Tier), "Iteration: %s.Expected tier name to be: %s", ruleID.iteration, ruleID.expectedRuleID.Tier)
			Expect(ruleID.pendingRuleIDs[0].Namespace).To(Equal(ruleID.expectedRuleID.Namespace), "Iteration: %s.Expected namespace to be: %s", ruleID.iteration, ruleID.expectedRuleID.Namespace)
			Expect(ruleID.pendingRuleIDs[0].Action).To(Equal(ruleID.expectedRuleID.Action), "Iteration: %s.Expected action to be: %s", ruleID.iteration, ruleID.expectedRuleID.Action)
			Expect(ruleID.pendingRuleIDs[0].Direction).To(Equal(ruleID.expectedRuleID.Direction), "Iteration: %s.Expected direction to be: %s", ruleID.iteration, ruleID.expectedRuleID.Direction)
			Expect(ruleID.pendingRuleIDs[0].Index).To(Equal(ruleID.expectedRuleID.Index), "Iteration: %s.Expected index to be: %s", ruleID.iteration, ruleID.expectedRuleID.Index)
		}

		// Update the policies
		c.policyStoreManager.DoWithLock(func(ps *policystore.PolicyStore) {
			ps.Endpoints[types.WorkloadEndpointID{
				OrchestratorId: "k8s",
				WorkloadId:     "default.workload1",
				EndpointId:     "eth0",
			}] = &proto.WorkloadEndpoint{
				State: "active",
				Name:  "eth0",
				Tiers: []*proto.TierInfo{
					{
						Name:           "tier1",
						EgressPolicies: []string{"policy11"},
					},
				},
			}
			ps.Endpoints[types.WorkloadEndpointID{
				OrchestratorId: "k8s",
				WorkloadId:     "default.workload2",
				EndpointId:     "eth0",
			}] = &proto.WorkloadEndpoint{
				State: "active",
				Name:  "eth0",
				Tiers: []*proto.TierInfo{
					{
						Name:            "tier1",
						IngressPolicies: []string{"policy11"},
					},
				},
			}
		})

		// Update pending rule traces again
		c.updatePendingRuleTraces()

		// The pending rule traces should be updated for data1, but not data2
		for _, ruleID := range []struct {
			iteration      int
			pendingRuleIDs []*calc.RuleID
			expectedRuleID *calc.RuleID
		}{
			{0, data1.IngressPendingRuleIDs, tier1TierPolicy1AllowIngressRuleID},
			{1, data1.EgressPendingRuleIDs, tier1TierPolicy1DenyEgressRuleID},
			{2, data2.IngressPendingRuleIDs, tier1TierPolicy1AllowIngressRuleID},
			{3, data2.EgressPendingRuleIDs, tier1TierPolicy1DenyEgressRuleID},
		} {
			Expect(ruleID.pendingRuleIDs).To(HaveLen(1), "Iteration: %s. Expected PendingRuleIDs to be updated", ruleID.iteration)
			Expect(ruleID.pendingRuleIDs[0].Name).To(Equal(ruleID.expectedRuleID.Name), "Iteration: %s.Expected policy name to be: %s", ruleID.iteration, ruleID.expectedRuleID.Name)
			Expect(ruleID.pendingRuleIDs[0].Tier).To(Equal(ruleID.expectedRuleID.Tier), "Iteration: %s.Expected tier name to be: %s", ruleID.iteration, ruleID.expectedRuleID.Tier)
			Expect(ruleID.pendingRuleIDs[0].Namespace).To(Equal(ruleID.expectedRuleID.Namespace), "Iteration: %s.Expected namespace to be: %s", ruleID.iteration, ruleID.expectedRuleID.Namespace)
			Expect(ruleID.pendingRuleIDs[0].Action).To(Equal(ruleID.expectedRuleID.Action), "Iteration: %s.Expected action to be: %s", ruleID.iteration, ruleID.expectedRuleID.Action)
			Expect(ruleID.pendingRuleIDs[0].Direction).To(Equal(ruleID.expectedRuleID.Direction), "Iteration: %s.Expected direction to be: %s", ruleID.iteration, ruleID.expectedRuleID.Direction)
			Expect(ruleID.pendingRuleIDs[0].Index).To(Equal(ruleID.expectedRuleID.Index), "Iteration: %s.Expected index to be: %s", ruleID.iteration, ruleID.expectedRuleID.Index)
		}
	})

}

func TestEqualFunction(t *testing.T) {
	RegisterTestingT(t)
	t.Run("should return true for equal rule IDs", func(t *testing.T) {
		ruleID1 := &calc.RuleID{
			PolicyID: calc.PolicyID{
				Tier:      "default",
				Name:      "policy1",
				Namespace: "",
			},
			Index:     0,
			IndexStr:  "0",
			Action:    rules.RuleActionAllow,
			Direction: rules.RuleDirIngress,
		}
		ruleID2 := &calc.RuleID{
			PolicyID: calc.PolicyID{
				Tier:      "default",
				Name:      "policy1",
				Namespace: "",
			},
			Index:     0,
			IndexStr:  "0",
			Action:    rules.RuleActionAllow,
			Direction: rules.RuleDirIngress,
		}
		ruleID3 := &calc.RuleID{
			PolicyID: calc.PolicyID{
				Tier:      "default",
				Name:      "policy2",
				Namespace: "",
			},
			Index:     1,
			IndexStr:  "1",
			Action:    rules.RuleActionAllow,
			Direction: rules.RuleDirEgress,
		}
		ruleID4 := &calc.RuleID{
			PolicyID: calc.PolicyID{
				Tier:      "default",
				Name:      "policy2",
				Namespace: "",
			},
			Index:     1,
			IndexStr:  "1",
			Action:    rules.RuleActionAllow,
			Direction: rules.RuleDirEgress,
		}

		Expect(equal([]*calc.RuleID{ruleID1, ruleID3}, []*calc.RuleID{ruleID2, ruleID4})).To(BeTrue(), "Expected true, got false")
	})

	t.Run("should return false for rule IDs that contain the same elements but are out of order", func(t *testing.T) {
		ruleID1 := &calc.RuleID{
			PolicyID: calc.PolicyID{
				Tier:      "default",
				Name:      "policy1",
				Namespace: "",
			},
			Index:     0,
			IndexStr:  "0",
			Action:    rules.RuleActionAllow,
			Direction: rules.RuleDirIngress,
		}
		ruleID2 := &calc.RuleID{
			PolicyID: calc.PolicyID{
				Tier:      "default",
				Name:      "policy1",
				Namespace: "",
			},
			Index:     1,
			IndexStr:  "1",
			Action:    rules.RuleActionAllow,
			Direction: rules.RuleDirIngress,
		}
		ruleID3 := &calc.RuleID{
			PolicyID: calc.PolicyID{
				Tier:      "default",
				Name:      "policy1",
				Namespace: "",
			},
			Index:     1,
			IndexStr:  "1",
			Action:    rules.RuleActionAllow,
			Direction: rules.RuleDirIngress,
		}
		ruleID4 := &calc.RuleID{
			PolicyID: calc.PolicyID{
				Tier:      "default",
				Name:      "policy1",
				Namespace: "",
			},
			Index:     0,
			IndexStr:  "0",
			Action:    rules.RuleActionAllow,
			Direction: rules.RuleDirIngress,
		}

		Expect(equal([]*calc.RuleID{ruleID1, ruleID3}, []*calc.RuleID{ruleID2, ruleID4})).To(BeFalse(), "Expected false, got true")
	})

	t.Run("should return false for different lengths of rule IDs", func(t *testing.T) {
		ruleID1 := &calc.RuleID{
			PolicyID: calc.PolicyID{
				Tier:      "default",
				Name:      "policy1",
				Namespace: "",
			},
			Index:     0,
			IndexStr:  "0",
			Action:    rules.RuleActionAllow,
			Direction: rules.RuleDirIngress,
		}
		ruleID2 := &calc.RuleID{
			PolicyID: calc.PolicyID{
				Tier:      "default",
				Name:      "policy1",
				Namespace: "",
			},
			Index:     0,
			IndexStr:  "0",
			Action:    rules.RuleActionAllow,
			Direction: rules.RuleDirIngress,
		}
		ruleID3 := &calc.RuleID{
			PolicyID: calc.PolicyID{
				Tier:      "default",
				Name:      "policy1",
				Namespace: "",
			},
			Index:     1,
			IndexStr:  "1",
			Action:    rules.RuleActionAllow,
			Direction: rules.RuleDirIngress,
		}

		if equal([]*calc.RuleID{ruleID1, ruleID3}, []*calc.RuleID{ruleID2}) {
			t.Errorf("Expected false, got true")
		}
	})
}
