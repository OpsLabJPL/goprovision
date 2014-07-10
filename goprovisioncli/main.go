package main

import (
	"fmt"
	"github.com/crowdmob/goamz/aws"
	"github.com/crowdmob/goamz/ec2"
	"github.com/opslabjpl/goprovision"
)

func main() {
	auth, err := aws.EnvAuth()
	if err != nil {
		panic(err.Error())
	}
	provisioner := goprovision.Provisioner{
		EC2: ec2.New(auth, aws.USEast),
	}

	filter := ec2.NewFilter()
	filter.Add("tag-value", "helloworld")
	filter.Add("tag-key", "comsolcloud")

	instances, _ := provisioner.Instances(nil, filter)
	fmt.Println(instances)
	instIds := goprovision.InstObjsToIds(instances)
	fmt.Println(instIds)
	//provisioner.EC2.TerminateInstances(instIds)
}

func main_run() {
	auth, err := aws.EnvAuth()
	if err != nil {
		panic(err.Error())
	}

	// Options for how to create EC2 instances
	ec2Opts := ec2.RunInstancesOptions{
		ImageId:      "ami-83e4bcea",
		InstanceType: "t1.micro",
		MinCount:     2,
		KeyName:      "ddao",
	}

	// Additional options for the provisioning
	provOpts := goprovision.ProvOpts{
		Tags:               []ec2.Tag{{"comsolcloud", ""}, {"ddao", "helloworld"}},
		TagAttachedVolumes: true,
		SelfShutdown:       true,
		KeepAliveProcesses: []string{"top", "free"},
		KeepAliveOpts:      "300 30",

		// User can specify their own startup script to run
		StartUpScript: "./startup",
	}

	provisioner := goprovision.Provisioner{
		EC2: ec2.New(auth, aws.USEast),
	}

	instanceIds, err := provisioner.CreateInstances(ec2Opts, provOpts)
	if err != nil {
		panic(err.Error())
	} else {
		println(instanceIds)
	}
}
