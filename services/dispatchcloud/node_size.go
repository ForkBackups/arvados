// Copyright (C) The Arvados Authors. All rights reserved.
//
// SPDX-License-Identifier: AGPL-3.0

package dispatchcloud

import (
	"bytes"
	"errors"
	"log"
	"os/exec"
	"strings"
	"time"

	"git.curoverse.com/arvados.git/sdk/go/arvados"
)

var (
	ErrConstraintsNotSatisfiable  = errors.New("constraints not satisfiable by any configured instance type")
	ErrInstanceTypesNotConfigured = errors.New("site configuration does not list any instance types")
)

// ChooseInstanceType returns the cheapest available
// arvados.InstanceType big enough to run ctr.
func ChooseInstanceType(cc *arvados.Cluster, ctr *arvados.Container) (best arvados.InstanceType, err error) {
	needVCPUs := ctr.RuntimeConstraints.VCPUs
	needRAM := ctr.RuntimeConstraints.RAM + ctr.RuntimeConstraints.KeepCacheRAM

	if len(cc.InstanceTypes) == 0 {
		err = ErrInstanceTypesNotConfigured
		return
	}

	err = ErrConstraintsNotSatisfiable
	for _, it := range cc.InstanceTypes {
		switch {
		case err == nil && it.Price > best.Price:
		case it.RAM < needRAM:
		case it.VCPUs < needVCPUs:
		case it.Price == best.Price && (it.RAM < best.RAM || it.VCPUs < best.VCPUs):
			// Equal price, but worse specs
		default:
			// Lower price || (same price && better specs)
			best = it
			err = nil
		}
	}
	return
}

// SlurmNodeTypeFeatureKludge ensures SLURM accepts every instance
// type name as a valid feature name, even if no instances of that
// type have appeared yet.
//
// It takes advantage of some SLURM peculiarities:
//
// (1) A feature is valid after it has been offered by a node, even if
// it is no longer offered by any node. So, to make a feature name
// valid, we can add it to a dummy node ("compute0"), then remove it.
//
// (2) when srun is given an invalid --gres argument and an invalid
// --constraint argument, the error message mentions "Invalid feature
// specification". So, to test whether a feature name is valid without
// actually submitting a job, we can call srun with the feature name
// and an invalid --gres argument.
//
// SlurmNodeTypeFeatureKludge does a test-and-fix operation
// immediately, and then periodically, in case slurm restarts and
// forgets the list of valid features. It never returns, so it should
// generally be invoked with "go".
func SlurmNodeTypeFeatureKludge(cc *arvados.Cluster) {
	var types []string
	for _, it := range cc.InstanceTypes {
		types = append(types, it.Name)
	}
	for {
		slurmKludge(types)
		time.Sleep(time.Minute)
	}
}

var (
	slurmDummyNode     = "compute0"
	slurmErrBadFeature = "Invalid feature"
	slurmErrBadGres    = "Invalid generic resource"
)

func slurmKludge(types []string) {
	cmd := exec.Command("srun", "--gres=invalid-gres-specification", "--constraint="+strings.Join(types, "&"), "true")
	out, err := cmd.CombinedOutput()
	switch {
	case err == nil:
		log.Printf("warning: guaranteed-to-fail srun command did not fail: %q %q", cmd.Path, cmd.Args)
		log.Printf("output was: %q", out)

	case bytes.Contains(out, []byte(slurmErrBadFeature)):
		log.Printf("temporarily configuring node %q with all node type features", slurmDummyNode)
		for _, features := range []string{strings.Join(types, ","), ""} {
			cmd = exec.Command("scontrol", "update", "NodeName="+slurmDummyNode, "Features="+features)
			log.Printf("running: %q %q", cmd.Path, cmd.Args)
			out, err := cmd.CombinedOutput()
			if err != nil {
				log.Printf("error: scontrol: %s (output was %q)", err, out)
			}
		}

	case bytes.Contains(out, []byte(slurmErrBadGres)):
		// Evidently our node-type feature names are all valid.

	default:
		log.Printf("warning: expected srun error %q or %q, but output was %q", slurmErrBadFeature, slurmErrBadGres, out)
	}
}
