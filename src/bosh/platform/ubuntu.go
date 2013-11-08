package platform

import (
	bosherr "bosh/errors"
	boshdisk "bosh/platform/disk"
	boshstats "bosh/platform/stats"
	boshsettings "bosh/settings"
	boshsys "bosh/system"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"
)

type ubuntu struct {
	collector       boshstats.StatsCollector
	fs              boshsys.FileSystem
	cmdRunner       boshsys.CmdRunner
	partitioner     boshdisk.Partitioner
	formatter       boshdisk.Formatter
	mounter         boshdisk.Mounter
	diskWaitTimeout time.Duration
}

func newUbuntuPlatform(collector boshstats.StatsCollector, fs boshsys.FileSystem, cmdRunner boshsys.CmdRunner, diskManager boshdisk.Manager) (platform ubuntu) {
	platform.collector = collector
	platform.fs = fs
	platform.cmdRunner = cmdRunner
	platform.partitioner = diskManager.GetPartitioner()
	platform.formatter = diskManager.GetFormatter()
	platform.mounter = diskManager.GetMounter()
	platform.diskWaitTimeout = 3 * time.Minute
	return
}

func (p ubuntu) GetStatsCollector() (statsCollector boshstats.StatsCollector) {
	return p.collector
}

func (p ubuntu) SetupRuntimeConfiguration() (err error) {
	_, _, err = p.cmdRunner.RunCommand("bosh-agent-rc")
	return
}

func (p ubuntu) SetupSsh(publicKey, username string) (err error) {
	homeDir, err := p.fs.HomeDir(username)
	if err != nil {
		return bosherr.WrapError(err, "Error finding home dir for user")
	}

	sshPath := filepath.Join(homeDir, ".ssh")
	p.fs.MkdirAll(sshPath, os.FileMode(0700))
	p.fs.Chown(sshPath, username)

	authKeysPath := filepath.Join(sshPath, "authorized_keys")
	_, err = p.fs.WriteToFile(authKeysPath, publicKey)
	if err != nil {
		return bosherr.WrapError(err, "Error creating authorized_keys file")
	}

	p.fs.Chown(authKeysPath, username)
	p.fs.Chmod(authKeysPath, os.FileMode(0600))

	return
}

func (p ubuntu) SetupDhcp(networks boshsettings.Networks) (err error) {
	dnsServers := []string{}
	dnsNetwork, found := networks.DefaultNetworkFor("dns")
	if found {
		for i := len(dnsNetwork.Dns) - 1; i >= 0; i-- {
			dnsServers = append(dnsServers, dnsNetwork.Dns[i])
		}
	}

	type dhcpConfigArg struct {
		DnsServers []string
	}

	buffer := bytes.NewBuffer([]byte{})
	t := template.Must(template.New("dhcp-config").Parse(DHCP_CONFIG_TEMPLATE))

	err = t.Execute(buffer, dhcpConfigArg{dnsServers})
	if err != nil {
		return
	}

	written, err := p.fs.WriteToFile("/etc/dhcp3/dhclient.conf", buffer.String())
	if err != nil {
		return
	}

	if written {
		// Ignore errors here, just run the commands
		p.cmdRunner.RunCommand("pkill", "dhclient3")
		p.cmdRunner.RunCommand("/etc/init.d/networking", "restart")
	}

	return
}

// DHCP Config file - /etc/dhcp3/dhclient.conf
const DHCP_CONFIG_TEMPLATE = `# Generated by bosh-agent

option rfc3442-classless-static-routes code 121 = array of unsigned integer 8;

send host-name "<hostname>";

request subnet-mask, broadcast-address, time-offset, routers,
	domain-name, domain-name-servers, domain-search, host-name,
	netbios-name-servers, netbios-scope, interface-mtu,
	rfc3442-classless-static-routes, ntp-servers;

{{ range .DnsServers }}prepend domain-name-servers {{ . }};
{{ end }}`

func (p ubuntu) SetupEphemeralDiskWithPath(devicePath, mountPoint string) (err error) {
	p.fs.MkdirAll(mountPoint, os.FileMode(0750))

	realPath, err := p.getRealDevicePath(devicePath)
	if err != nil {
		return
	}

	swapSize, linuxSize, err := p.calculateEphemeralDiskPartitionSizes(realPath)
	if err != nil {
		return
	}

	partitions := []boshdisk.Partition{
		{SizeInBlocks: swapSize, Type: boshdisk.PartitionTypeSwap},
		{SizeInBlocks: linuxSize, Type: boshdisk.PartitionTypeLinux},
	}

	err = p.partitioner.Partition(realPath, partitions)
	if err != nil {
	    return
	}

	swapPartitionPath := realPath + "1"
	dataPartitionPath := realPath + "2"
	err = p.formatter.Format(swapPartitionPath, boshdisk.FileSystemSwap)
	if err != nil {
		return
	}

	err = p.formatter.Format(dataPartitionPath, boshdisk.FileSystemExt4)
	if err != nil {
		return
	}

	err = p.mounter.SwapOn(swapPartitionPath)
	if err != nil {
		return
	}

	err = p.mounter.Mount(dataPartitionPath, mountPoint)
	if err != nil {
		return
	}

	err = p.fs.MkdirAll(filepath.Join(mountPoint, "sys", "log"), os.FileMode(0750))
	if err != nil {
		return
	}

	err = p.fs.MkdirAll(filepath.Join(mountPoint, "sys", "run"), os.FileMode(0750))
	if err != nil {
		return
	}
	return
}

func (p ubuntu) getRealDevicePath(devicePath string) (realPath string, err error) {
	stopAfter := time.Now().Add(p.diskWaitTimeout)

	realPath, found := p.findPossibleDevice(devicePath)
	for !found {
		if time.Now().After(stopAfter) {
			err = errors.New(fmt.Sprintf("Timed out getting real device path for %s", devicePath))
			return
		}
		time.Sleep(100 * time.Millisecond)
		realPath, found = p.findPossibleDevice(devicePath)
	}

	return
}

func (p ubuntu) findPossibleDevice(devicePath string) (realPath string, found bool) {
	pathSuffix := strings.Split(devicePath, "/dev/sd")[1]

	possiblePrefixes := []string{"/dev/xvd", "/dev/vd", "/dev/sd"}
	for _, prefix := range possiblePrefixes {
		path := prefix + pathSuffix
		if p.fs.FileExists(path) {
			realPath = path
			found = true
			return
		}
	}
	return
}

func (p ubuntu) calculateEphemeralDiskPartitionSizes(devicePath string) (swapSize, linuxSize uint64, err error) {
	memStats, err := p.collector.GetMemStats()
	if err != nil {
		return
	}

	blockSizeInBytes := uint64(512)
	totalMemInKb := memStats.Total
	totalMemInBlocks := totalMemInKb * uint64(1024) / blockSizeInBytes

	diskSizeInBlocks, err := p.partitioner.GetDeviceSizeInBlocks(devicePath)
	if err != nil {
		return
	}

	if totalMemInBlocks > diskSizeInBlocks/2 {
		swapSize = diskSizeInBlocks / 2
	} else {
		swapSize = totalMemInBlocks
	}

	linuxSize = diskSizeInBlocks - swapSize
	return
}
