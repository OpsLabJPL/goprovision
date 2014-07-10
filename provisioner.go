package goprovision

import (
	"github.com/op/go-logging"
	"github.com/opslabjpl/goamz/ec2"
	"io/ioutil"
	"net"
	"strings"
	"time"
)

var logger = logging.MustGetLogger("opslabcloud.goprovision")

type Provisioner struct {
	EC2 *ec2.EC2
}

type ProvOpts struct {
	Tags               []ec2.Tag
	TagAttachedVolumes bool
	SelfShutdown       bool
	KeepAliveProcesses []string // keep machine alive if these processes are running
	KeepAliveOpts      string   // e.g. "300 30"
	// 300 is the threshold (in seconds)
	// 30 is the frequency of the check (in seconds)
	StartUpScript string
	UserDataFile  string
}

func (pro *Provisioner) CreateInstances(ec2Opts ec2.RunInstancesOptions, provOpts ProvOpts) (instances []ec2.Instance, err error) {
	e := pro.EC2
	var instanceIds []string

	if provOpts.UserDataFile != "" {
		ec2Opts.UserData = PrepUserData(provOpts.UserDataFile, provOpts)
	}

	resp, err := e.RunInstances(&ec2Opts)
	if err != nil {
		return
	}

	for _, instance := range resp.Instances {
		instanceIds = append(instanceIds, instance.InstanceId)
		instances = append(instances, instance)
	}

	// tag the instance if needed
	tags := provOpts.Tags
	if tags != nil {
		logger.Debug("Tagging instance with %v", tags)
		e.CreateTags(instanceIds, tags)
	}

	// tag attached volumes if needed
	if provOpts.TagAttachedVolumes == true {
		// TODO: run in different goroutines
		for _, instanceId := range instanceIds {
			pro.TagAttachedVolumes(instanceId, tags)
		}
	}
	return
}

// Given an instance id, return list of ebs volume ids associated with
// the instance
func (pro *Provisioner) GetAttachedEBSVolumeIds(instanceId string) (volumeIds []string) {
	instances, err := pro.Instances([]string{instanceId}, nil)
	if err != nil {
		panic(err.Error())
	}

	for _, instance := range instances {
		for _, blockDevice := range instance.BlockDevices {
			volumeIds = append(volumeIds, blockDevice.EBS.VolumeId)
		}
	}
	return
}

// Given an instance id, tag its attached EBS volumes
func (pro *Provisioner) TagAttachedVolumes(instanceId string, tags []ec2.Tag) {
	e := pro.EC2

	// wait until we get list of EBS volumes
	// TODO: worry about timeout and remove hardcode
	volumeIds := pro.GetAttachedEBSVolumeIds(instanceId)
	for ; volumeIds == nil; volumeIds = pro.GetAttachedEBSVolumeIds(instanceId) {
		logger.Debug("waiting for volumes to show up for " + instanceId)
		time.Sleep(5 * time.Second)
	}

	logger.Debug("Tagging volume with tags %v", tags)
	_, err2 := e.CreateTags(volumeIds, tags)
	if err2 != nil {
		panic(err2.Error())
	}
}

// Wait until all of the given instances are up and running
// Return a new list of instances that have all of the latest info
// (e.g. IPAddress, EBS volumes, etc)
func (pro *Provisioner) WaitTillAllRunning(instances []ec2.Instance) (instances2 []ec2.Instance) {
	instanceIds := InstObjsToIds(instances)

	running := 0
	for {
		running = 0
		instances2, _ = pro.Instances(instanceIds, nil)

		for _, instance := range instances2 {
			// fmt.Println("state is", instance.State.Name)
			if instance.State.Name == "running" {
				running++
			}
		}
		if running == len(instances2) {
			return
		}
		time.Sleep(5 * time.Second)
	}
	return
}

// Wait until all of the instances are SSH'able
func (pro *Provisioner) WaitTillSSHable(waitInstances []ec2.Instance, timeoutSec time.Duration, privateIP bool) (instances []ec2.Instance) {
	logger.Info("Waiting for machine(s) to be accessible via SSH.")
	// First, we need for machines to be in running state and show up with ip addresses
	instances = pro.WaitTillAllRunning(waitInstances)

	fanin := make(chan string, len(instances))
	timeout := time.After(time.Second * timeoutSec)

	for _, instance := range instances {
		ipaddr := instance.IPAddress
		if privateIP == true {
			ipaddr = instance.PrivateIPAddress
		}
		go func(instance string) {
			loopForSSH(instance)
			fanin <- instance
		}(ipaddr)
	}
	for count := 0; count < len(instances); count++ {
		select {
		case partial := <-fanin:
			logger.Debug(partial + " now has SSH running")
		case <-timeout:
			panic("Unable to connect to all of the machines")
		}
	}

	return
}

// Same as EC2.Instances but return []ec2.Instance instead of InstancesResp since
// that's what we ultimately want 99% of the time
func (pro *Provisioner) Instances(instanceIds []string, filter *ec2.Filter) (instances []ec2.Instance, err error) {
	instancesResp, err := pro.EC2.DescribeInstances(instanceIds, filter)
	if err != nil {
		logger.Error(err.Error())
	}
	instances = InstancesRespToInstances(*instancesResp)
	return
}

////////////////// Utility functions ///////////////
// Generate UserData []byte
func PrepUserData(userDataFile string, opts ProvOpts) (userData []byte) {
	userData, err := ioutil.ReadFile(userDataFile)
	if err != nil {
		panic(err.Error())
	}

	userDataStr := string(userData)

	// whether or not we need to set up and run the terminator script
	if opts.SelfShutdown == true {
		keepAliveProcesses := strings.Join(opts.KeepAliveProcesses, "|")
		userDataStr = strings.Replace(userDataStr, "__KEEPALIVE_PROCESSES__", keepAliveProcesses, -1)
		userDataStr = strings.Replace(userDataStr, "__TERMINATOR_OPS__", "/opt/encirrus/isalive "+opts.KeepAliveOpts, -1)
		userDataStr = strings.Replace(userDataStr, "__RUN_TERMINATOR__", "true", -1)
	} else {
		userDataStr = strings.Replace(userDataStr, "__RUN_TERMINATOR__", "", -1)
	}

	if opts.StartUpScript != "" {
		startUpScript, _ := ioutil.ReadFile(opts.StartUpScript)
		userDataStr = strings.Replace(userDataStr, "__STARTUP_SCRIPT__", string(startUpScript), -1)
	}

	return []byte(userDataStr)
}

// since Go doesn't have list comprension like Ruby or Python, this method
// makes it easier to convert from array of Instances to array of instances IDs
func InstObjsToIds(instances []ec2.Instance) (instanceIds []string) {
	for _, instance := range instances {
		instanceIds = append(instanceIds, instance.InstanceId)
	}
	return
}

// convert InstanceResp to []ec2.Instances
func InstancesRespToInstances(instancesResp ec2.DescribeInstancesResp) (instances []ec2.Instance) {
	for _, reservation := range instancesResp.Reservations {
		for _, instance := range reservation.Instances {
			instances = append(instances, instance)
		}
	}
	return
}

// Check whether or not SSH service is running on the remote
// host
func isSSHRunning(hostname string) bool {
	hostname = hostname + ":22"
	_, err := net.DialTimeout("tcp", hostname, time.Second)
	return err == nil
}

// keep on looping until SSH service is up and running
// on the given host
func loopForSSH(hostname string) {
	for {
		if isSSHRunning(hostname) == true {
			return
		} else {
			time.Sleep(time.Second * 5)
		}
	}
}
