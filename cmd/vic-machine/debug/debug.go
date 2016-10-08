// Copyright 2016 VMware, Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package debug

import (
	"io/ioutil"

	log "github.com/Sirupsen/logrus"

	"github.com/urfave/cli"
	"github.com/vmware/vic/lib/install/data"
	"github.com/vmware/vic/lib/install/management"
	"github.com/vmware/vic/lib/install/validate"
	"github.com/vmware/vic/pkg/errors"
	"github.com/vmware/vic/pkg/trace"
	"github.com/vmware/vic/pkg/version"
	"github.com/vmware/vic/pkg/vsphere/vm"

	"golang.org/x/net/context"
)

// Debug has all input parameters for vic-machine Debug command
type Debug struct {
	*data.Data

	executor *management.Dispatcher

	enableSSH     bool
	password      string
	authorizedKey string
}

func NewDebug() *Debug {
	d := &Debug{}
	d.Data = data.NewData()
	return d
}

// Flags return all cli flags for Debug
func (d *Debug) Flags() []cli.Flag {
	preFlags := append(d.TargetFlags(), d.IDFlags()...)
	preFlags = append(preFlags, d.ComputeFlags()...)

	flags := []cli.Flag{
		cli.BoolFlag{
			Name:        "enable-ssh, ssh",
			Usage:       "Enable SSH server within appliance VM",
			Destination: &d.enableSSH,
		},
		cli.StringFlag{
			Name:        "authorized-key, key",
			Value:       "",
			Usage:       "File with public key to place as /root/.ssh/authorized_keys",
			Destination: &d.authorizedKey,
		},
		cli.StringFlag{
			Name:        "rootpw, pw",
			Value:       "",
			Usage:       "Password to set for root user (non-persistent over reboots)",
			Destination: &d.password,
		},
	}

	flags = append(preFlags, flags...)

	return append(flags, d.DebugFlags()...)
}

func (d *Debug) processParams() error {
	defer trace.End(trace.Begin(""))

	if err := d.HasCredentials(); err != nil {
		return err
	}

	d.Insecure = true
	return nil
}

func (d *Debug) Run(cli *cli.Context) error {
	var err error
	if err = d.processParams(); err != nil {
		return err
	}

	if d.Debug.Debug > 0 {
		log.SetLevel(log.DebugLevel)
		trace.Logger.Level = log.DebugLevel
	}

	if len(cli.Args()) > 0 {
		log.Errorf("Unknown argument: %s", cli.Args()[0])
		return errors.New("invalid CLI arguments")
	}

	log.Infof("### Configuring VCH for debug ####")

	ctx, cancel := context.WithTimeout(context.Background(), d.Timeout)
	defer cancel()

	validator, err := validate.NewValidator(ctx, d.Data)
	if err != nil {
		log.Errorf("Debug cannot continue - failed to create validator: %s", err)
		return errors.New("Debug failed")
	}
	executor := management.NewDispatcher(validator.Context, validator.Session, nil, d.Force)

	var vch *vm.VirtualMachine
	if d.Data.ID != "" {
		vch, err = executor.NewVCHFromID(d.Data.ID)
	} else {
		vch, err = executor.NewVCHFromComputePath(d.Data.ComputeResourcePath, d.Data.DisplayName, validator)
	}
	if err != nil {
		log.Errorf("Failed to get Virtual Container Host %s", d.DisplayName)
		log.Error(err)
		return errors.New("Debug failed")
	}

	log.Infof("")
	log.Infof("VCH ID: %s", vch.Reference().String())

	vchConfig, err := executor.GetVCHConfig(vch)
	if err != nil {
		log.Error("Failed to get Virtual Container Host configuration")
		log.Error(err)
		return errors.New("Debug failed")
	}
	executor.InitDiagnosticLogs(vchConfig)

	installerVer := version.GetBuild()

	log.Info("")
	log.Infof("Installer version: %s", installerVer.ShortVersion())
	log.Infof("VCH version: %s", vchConfig.Version.ShortVersion())

	// load the key file if set
	var key []byte
	if d.authorizedKey != "" {
		key, err = ioutil.ReadFile(d.authorizedKey)
		if err != nil {
			log.Errorf("Unable to read public key from %s: %s", d.authorizedKey, err)
			return errors.New("unable to load public key")
		}
	}

	if err = executor.DebugVCH(vch, vchConfig, d.password, string(key)); err != nil {
		executor.CollectDiagnosticLogs()
		log.Errorf("%s", err)
		return errors.New("Debug failed")
	}

	// display the VCH endpoints again for convenience
	if err = executor.InspectVCH(vch, vchConfig); err != nil {
		executor.CollectDiagnosticLogs()
		log.Errorf("%s", err)
		return errors.New("inspect failed")
	}

	log.Infof("Completed successfully")

	return nil
}