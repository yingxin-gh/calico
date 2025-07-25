// Copyright (c) 2025 Tigera, Inc. All rights reserved.
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

package intdataplane

import (
	"errors"
	"fmt"

	apiv3 "github.com/projectcalico/api/pkg/apis/projectcalico/v3"

	"github.com/projectcalico/calico/felix/bpf/tc"
	"github.com/projectcalico/calico/felix/dataplane/linux/qos"
	"github.com/projectcalico/calico/felix/proto"
)

// Bandwidth QoS controls are supported on iptables and nftables modes, and on BPF mode if 'tcx' attach mode is used.
func (m *endpointManager) isQoSBandwidthSupported() bool {
	return !m.bpfEnabled || (m.bpfEnabled && m.bpfAttachType == string(apiv3.BPFAttachOptionTCX) && tc.IsTcxSupported())
}

func (m *endpointManager) maybeUpdateQoSBandwidth(old, new *proto.WorkloadEndpoint) error {
	var errs []error

	var oldName, newName string

	if old != nil {
		oldName = old.Name
	}
	if new != nil {
		newName = new.Name
	}

	if old != nil && (oldName != newName) {
		// Interface name changed, or workload removed.  Remove ingress QoS, if present,
		// from the old workload interface.
		oldIngress, err := qos.ReadIngressQdisc(oldName)
		if err != nil {
			errs = append(errs, fmt.Errorf("error reading ingress qdisc from workload %s: %w", oldName, err))
		}
		if oldIngress != nil {
			err := qos.RemoveIngressQdisc(oldName)
			if err != nil {
				errs = append(errs, fmt.Errorf("error removing ingress qdisc from workload %s: %w", oldName, err))
			}
		}
		oldEgress, err := qos.ReadEgressQdisc(oldName)
		if err != nil {
			errs = append(errs, fmt.Errorf("error reading egress qdisc from workload %s: %w", oldName, err))
		}
		if oldEgress != nil {
			err := qos.RemoveEgressQdisc(oldName)
			if err != nil {
				errs = append(errs, fmt.Errorf("error removing egress qdisc from workload %s: %w", oldName, err))
			}
		}
	}

	// Now we are only concerned with the new workload interface.
	if new != nil {
		// Work out what we QoS we want.
		var desiredIngress, desiredEgress *qos.TokenBucketState
		if new.QosControls != nil {
			if new.QosControls.IngressBandwidth != 0 {
				desiredIngress = qos.GetTBFValues(uint64(new.QosControls.IngressBandwidth), uint64(new.QosControls.IngressBurst), uint64(new.QosControls.IngressPeakrate), uint32(new.QosControls.IngressMinburst))
			}
			if new.QosControls.EgressBandwidth != 0 {
				desiredEgress = qos.GetTBFValues(uint64(new.QosControls.EgressBandwidth), uint64(new.QosControls.EgressBurst), uint64(new.QosControls.EgressPeakrate), uint32(new.QosControls.EgressMinburst))
			}
		}

		// Read what QoS is currently set on the interface.
		currentIngress, err := qos.ReadIngressQdisc(newName)
		if err != nil {
			errs = append(errs, fmt.Errorf("error reading ingress qdisc from workload %s: %w", newName, err))
		}
		currentEgress, err := qos.ReadEgressQdisc(newName)
		if err != nil {
			errs = append(errs, fmt.Errorf("error reading egress qdisc from workload %s: %w", newName, err))
		}

		if currentIngress == nil && desiredIngress != nil {
			// Add.
			err := qos.CreateIngressQdisc(desiredIngress, newName)
			if err != nil {
				errs = append(errs, fmt.Errorf("error adding ingress qdisc to workload %s: %w", newName, err))
			}
		} else if currentIngress != nil && desiredIngress == nil {
			// Remove.
			err := qos.RemoveIngressQdisc(newName)
			if err != nil {
				errs = append(errs, fmt.Errorf("error removing ingress qdisc from workload %s: %w", newName, err))
			}
		} else if !currentIngress.Equals(desiredIngress) {
			// Update.
			err := qos.UpdateIngressQdisc(desiredIngress, newName)
			if err != nil {
				errs = append(errs, fmt.Errorf("error changing ingress qdisc on workload %s: %w", newName, err))
			}
		}

		if currentEgress == nil && desiredEgress != nil {
			// Add.
			err := qos.AddEgressQdisc(desiredEgress, newName)
			if err != nil {
				errs = append(errs, fmt.Errorf("error adding egress qdisc to workload %s: %w", newName, err))
			}
		} else if currentEgress != nil && desiredEgress == nil {
			// Remove.
			err := qos.RemoveEgressQdisc(newName)
			if err != nil {
				errs = append(errs, fmt.Errorf("error removing egress qdisc from workload %s: %w", newName, err))
			}
		} else if !currentEgress.Equals(desiredEgress) {
			// Update.
			err := qos.UpdateEgressQdisc(desiredEgress, qos.GetIfbDeviceName(newName))
			if err != nil {
				errs = append(errs, fmt.Errorf("error changing egress qdisc on workload %s: %w", newName, err))
			}
		}
	}

	return errors.Join(errs...)
}
