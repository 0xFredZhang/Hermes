// Package pulumiengine builds Hermes blueprints into real AWS resources via the
// Pulumi Automation API. Named pulumiengine (not pulumi) to avoid clashing with
// the Pulumi SDK's own "pulumi" package.
package pulumiengine

import (
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/elasticache"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/rds"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
)

// buildProgram returns a Pulumi inline program declaring the blueprint's
// network, security group, EC2 instances, and optional backing services.
func buildProgram(spec provisioner.Spec) pulumi.RunFunc {
	return func(ctx *pulumi.Context) error {
		p := spec.Params
		p.ApplyDefaults()
		amiID := p.EC2.AMI
		if amiID == "" {
			it, err := ec2.GetInstanceType(ctx, &ec2.GetInstanceTypeArgs{InstanceType: p.EC2.InstanceType})
			if err != nil {
				return err
			}
			arch := resolveArch(it.SupportedArchitectures)
			ami, err := ec2.LookupAmi(ctx, &ec2.LookupAmiArgs{
				MostRecent: pulumi.BoolRef(true),
				Owners:     []string{ubuntuOwner},
				Filters: []ec2.GetAmiFilter{
					{Name: "name", Values: []string{ubuntuNameFilter(arch)}},
					{Name: "state", Values: []string{"available"}},
				},
			})
			if err != nil {
				return err
			}
			amiID = ami.Id
		}

		network, err := declareNetwork(ctx, p.Network)
		if err != nil {
			return err
		}
		subnetID := network.instanceSubnetID

		ingress := ec2.SecurityGroupIngressArray{}
		for _, in := range p.SecurityGroup.Ingress {
			ingress = append(ingress, ec2.SecurityGroupIngressArgs{
				Protocol:    pulumi.String(in.Protocol),
				FromPort:    pulumi.Int(in.Port),
				ToPort:      pulumi.Int(in.Port),
				CidrBlocks:  pulumi.StringArray{pulumi.String(in.CIDR)},
				Description: pulumi.String(in.Desc),
			})
		}
		sg, err := ec2.NewSecurityGroup(ctx, "hermes-sg", &ec2.SecurityGroupArgs{
			VpcId:   network.vpcID,
			Ingress: ingress,
			Egress: ec2.SecurityGroupEgressArray{
				ec2.SecurityGroupEgressArgs{
					Protocol:   pulumi.String("-1"),
					FromPort:   pulumi.Int(0),
					ToPort:     pulumi.Int(0),
					CidrBlocks: pulumi.StringArray{pulumi.String("0.0.0.0/0")},
				},
			},
		})
		if err != nil {
			return err
		}

		if p.RDS.Enabled {
			if spec.Secrets.RDSPassword == "" {
				return fmt.Errorf("rds password is required when RDS is enabled")
			}
			if err := declareRDS(ctx, p.RDS, network.vpcID, network.subnetIDs, sg.ID().ToStringOutput(), spec.Secrets.RDSPassword); err != nil {
				return err
			}
		}
		if p.Redis.Enabled {
			if p.Redis.AuthEnabled && spec.Secrets.RedisAuthToken == "" {
				return fmt.Errorf("redis auth token is required when Redis auth is enabled")
			}
			if err := declareRedis(ctx, p.Redis, network.vpcID, network.subnetIDs, sg.ID().ToStringOutput(), spec.Secrets.RedisAuthToken); err != nil {
				return err
			}
		}

		var ids, ips, dns pulumi.StringArray
		for i := 0; i < p.EC2.Count; i++ {
			args := &ec2.InstanceArgs{
				Ami:                 pulumi.String(amiID),
				InstanceType:        pulumi.String(p.EC2.InstanceType),
				SubnetId:            subnetID,
				VpcSecurityGroupIds: pulumi.StringArray{sg.ID()},
				RootBlockDevice: &ec2.InstanceRootBlockDeviceArgs{
					VolumeSize: pulumi.Int(p.EC2.RootVolumeGB),
				},
			}
			if p.EC2.KeyName != "" {
				args.KeyName = pulumi.String(p.EC2.KeyName)
			}
			inst, err := ec2.NewInstance(ctx, fmt.Sprintf("hermes-ec2-%d", i), args)
			if err != nil {
				return err
			}
			eip, err := ec2.NewEip(ctx, fmt.Sprintf("hermes-eip-%d", i), &ec2.EipArgs{
				Instance: inst.ID(),
				Domain:   pulumi.String("vpc"),
			})
			if err != nil {
				return err
			}
			ids = append(ids, inst.ID().ToStringOutput())
			ips = append(ips, eip.PublicIp)
			dns = append(dns, inst.PublicDns)
		}

		ctx.Export("instance_ids", ids)
		ctx.Export("public_ips", ips)
		ctx.Export("public_dns", dns)
		if p.Network.Enabled {
			ctx.Export("vpc_id", network.vpcID)
			ctx.Export("subnet_ids", network.subnetIDs)
		}
		return nil
	}
}

type networkResources struct {
	vpcID            pulumi.StringInput
	subnetIDs        pulumi.StringArray
	instanceSubnetID pulumi.StringInput
}

func declareNetwork(ctx *pulumi.Context, cfg provisioner.Network) (networkResources, error) {
	if cfg.Enabled {
		return declareManagedNetwork(ctx, cfg)
	}
	vpc, err := ec2.LookupVpc(ctx, &ec2.LookupVpcArgs{Default: pulumi.BoolRef(true)})
	if err != nil {
		return networkResources{}, err
	}
	subnets, err := ec2.GetSubnets(ctx, &ec2.GetSubnetsArgs{
		Filters: []ec2.GetSubnetsFilter{{Name: "vpc-id", Values: []string{vpc.Id}}},
	})
	if err != nil {
		return networkResources{}, err
	}
	if len(subnets.Ids) == 0 {
		return networkResources{}, fmt.Errorf("no subnets found in default vpc %s", vpc.Id)
	}
	subnetIDs := pulumi.StringArray{}
	for _, id := range subnets.Ids {
		subnetIDs = append(subnetIDs, pulumi.String(id))
	}
	return networkResources{
		vpcID:            pulumi.String(vpc.Id),
		subnetIDs:        subnetIDs,
		instanceSubnetID: pulumi.String(subnets.Ids[0]),
	}, nil
}

func declareManagedNetwork(ctx *pulumi.Context, cfg provisioner.Network) (networkResources, error) {
	zones, err := aws.GetAvailabilityZones(ctx, &aws.GetAvailabilityZonesArgs{
		State: pulumi.StringRef("available"),
	}, nil)
	if err != nil {
		return networkResources{}, err
	}
	if len(zones.Names) == 0 {
		return networkResources{}, fmt.Errorf("no availability zones found")
	}
	vpc, err := ec2.NewVpc(ctx, "hermes-vpc", &ec2.VpcArgs{
		CidrBlock:          pulumi.String(cfg.VPCCIDR),
		EnableDnsHostnames: pulumi.Bool(true),
		EnableDnsSupport:   pulumi.Bool(true),
		Tags: pulumi.StringMap{
			"Name": pulumi.String("hermes-vpc"),
		},
	})
	if err != nil {
		return networkResources{}, err
	}
	igw, err := ec2.NewInternetGateway(ctx, "hermes-igw", &ec2.InternetGatewayArgs{
		VpcId: vpc.ID(),
		Tags: pulumi.StringMap{
			"Name": pulumi.String("hermes-igw"),
		},
	})
	if err != nil {
		return networkResources{}, err
	}
	routeTable, err := ec2.NewRouteTable(ctx, "hermes-public-rt", &ec2.RouteTableArgs{
		VpcId: vpc.ID(),
		Routes: ec2.RouteTableRouteArray{
			ec2.RouteTableRouteArgs{
				CidrBlock: pulumi.String("0.0.0.0/0"),
				GatewayId: igw.ID(),
			},
		},
		Tags: pulumi.StringMap{
			"Name": pulumi.String("hermes-public-rt"),
		},
	})
	if err != nil {
		return networkResources{}, err
	}
	subnetIDs := pulumi.StringArray{}
	for i, cidr := range cfg.PublicSubnetCIDRs {
		zone := zones.Names[i%len(zones.Names)]
		subnet, err := ec2.NewSubnet(ctx, fmt.Sprintf("hermes-public-subnet-%d", i), &ec2.SubnetArgs{
			VpcId:               vpc.ID(),
			CidrBlock:           pulumi.String(cidr),
			AvailabilityZone:    pulumi.String(zone),
			MapPublicIpOnLaunch: pulumi.Bool(cfg.MapPublicIPOnLaunch),
			Tags: pulumi.StringMap{
				"Name": pulumi.String(fmt.Sprintf("hermes-public-%d", i)),
			},
		})
		if err != nil {
			return networkResources{}, err
		}
		if _, err := ec2.NewRouteTableAssociation(ctx, fmt.Sprintf("hermes-public-rta-%d", i), &ec2.RouteTableAssociationArgs{
			RouteTableId: routeTable.ID(),
			SubnetId:     subnet.ID(),
		}); err != nil {
			return networkResources{}, err
		}
		subnetIDs = append(subnetIDs, subnet.ID().ToStringOutput())
	}
	if len(subnetIDs) == 0 {
		return networkResources{}, fmt.Errorf("managed network requires at least one public subnet")
	}
	return networkResources{
		vpcID:            vpc.ID().ToStringOutput(),
		subnetIDs:        subnetIDs,
		instanceSubnetID: subnetIDs[0],
	}, nil
}

func declareRDS(ctx *pulumi.Context, cfg provisioner.RDS, vpcID pulumi.StringInput, subnetIDs pulumi.StringArray, appSG pulumi.StringOutput, password string) error {
	dbSG, err := ec2.NewSecurityGroup(ctx, "hermes-rds-sg", &ec2.SecurityGroupArgs{
		VpcId: vpcID,
		Egress: ec2.SecurityGroupEgressArray{
			ec2.SecurityGroupEgressArgs{
				Protocol:   pulumi.String("-1"),
				FromPort:   pulumi.Int(0),
				ToPort:     pulumi.Int(0),
				CidrBlocks: pulumi.StringArray{pulumi.String("0.0.0.0/0")},
			},
		},
	})
	if err != nil {
		return err
	}
	_, err = ec2.NewSecurityGroupRule(ctx, "hermes-rds-from-ec2", &ec2.SecurityGroupRuleArgs{
		Type:                  pulumi.String("ingress"),
		SecurityGroupId:       dbSG.ID(),
		SourceSecurityGroupId: appSG,
		Protocol:              pulumi.String("tcp"),
		FromPort:              pulumi.Int(cfg.Port),
		ToPort:                pulumi.Int(cfg.Port),
		Description:           pulumi.String("Hermes EC2 to MySQL"),
	})
	if err != nil {
		return err
	}
	subnetGroup, err := rds.NewSubnetGroup(ctx, "hermes-rds-subnets", &rds.SubnetGroupArgs{
		SubnetIds:   subnetIDs,
		Description: pulumi.String("Hermes RDS subnet group"),
	})
	if err != nil {
		return err
	}
	db, err := rds.NewInstance(ctx, "hermes-rds", &rds.InstanceArgs{
		AllocatedStorage:    pulumi.Int(cfg.AllocatedStorageGB),
		DbName:              pulumi.String(cfg.DBName),
		DbSubnetGroupName:   subnetGroup.Name,
		DeletionProtection:  pulumi.Bool(false),
		Engine:              pulumi.String(cfg.Engine),
		EngineVersion:       pulumi.String(cfg.EngineVersion),
		InstanceClass:       pulumi.String(cfg.InstanceClass),
		Password:            pulumi.ToSecret(pulumi.String(password)).(pulumi.StringOutput).ToStringPtrOutput(),
		Port:                pulumi.Int(cfg.Port),
		PubliclyAccessible:  pulumi.Bool(false),
		SkipFinalSnapshot:   pulumi.Bool(true),
		StorageEncrypted:    pulumi.Bool(true),
		Username:            pulumi.String(cfg.Username),
		VpcSecurityGroupIds: pulumi.StringArray{dbSG.ID()},
	})
	if err != nil {
		return err
	}
	ctx.Export("rds_endpoint", db.Endpoint)
	ctx.Export("rds_address", db.Address)
	ctx.Export("rds_port", db.Port)
	ctx.Export("rds_username", db.Username)
	return nil
}

func declareRedis(ctx *pulumi.Context, cfg provisioner.Redis, vpcID pulumi.StringInput, subnetIDs pulumi.StringArray, appSG pulumi.StringOutput, authToken string) error {
	cacheSG, err := ec2.NewSecurityGroup(ctx, "hermes-redis-sg", &ec2.SecurityGroupArgs{
		VpcId: vpcID,
		Egress: ec2.SecurityGroupEgressArray{
			ec2.SecurityGroupEgressArgs{
				Protocol:   pulumi.String("-1"),
				FromPort:   pulumi.Int(0),
				ToPort:     pulumi.Int(0),
				CidrBlocks: pulumi.StringArray{pulumi.String("0.0.0.0/0")},
			},
		},
	})
	if err != nil {
		return err
	}
	_, err = ec2.NewSecurityGroupRule(ctx, "hermes-redis-from-ec2", &ec2.SecurityGroupRuleArgs{
		Type:                  pulumi.String("ingress"),
		SecurityGroupId:       cacheSG.ID(),
		SourceSecurityGroupId: appSG,
		Protocol:              pulumi.String("tcp"),
		FromPort:              pulumi.Int(cfg.Port),
		ToPort:                pulumi.Int(cfg.Port),
		Description:           pulumi.String("Hermes EC2 to Redis"),
	})
	if err != nil {
		return err
	}
	subnetGroup, err := elasticache.NewSubnetGroup(ctx, "hermes-redis-subnets", &elasticache.SubnetGroupArgs{
		SubnetIds:   subnetIDs,
		Description: pulumi.String("Hermes Redis subnet group"),
	})
	if err != nil {
		return err
	}
	args := &elasticache.ReplicationGroupArgs{
		ApplyImmediately: pulumi.Bool(true),
		Description:      pulumi.String("Hermes Redis"),
		Engine:           pulumi.String(cfg.Engine),
		EngineVersion:    pulumi.String(cfg.EngineVersion),
		NodeType:         pulumi.String(cfg.NodeType),
		NumCacheClusters: pulumi.Int(cfg.NodeCount),
		Port:             pulumi.Int(cfg.Port),
		SecurityGroupIds: pulumi.StringArray{cacheSG.ID()},
		SubnetGroupName:  subnetGroup.Name,
	}
	if cfg.AuthEnabled {
		args.TransitEncryptionEnabled = pulumi.Bool(true)
		args.AuthToken = pulumi.ToSecret(pulumi.String(authToken)).(pulumi.StringOutput).ToStringPtrOutput()
		args.AuthTokenUpdateStrategy = pulumi.String("SET")
	}
	rg, err := elasticache.NewReplicationGroup(ctx, "hermes-redis", args)
	if err != nil {
		return err
	}
	ctx.Export("redis_primary_endpoint", rg.PrimaryEndpointAddress)
	ctx.Export("redis_reader_endpoint", rg.ReaderEndpointAddress)
	ctx.Export("redis_port", rg.Port)
	return nil
}

const ubuntuOwner = "099720109477" // Canonical (Ubuntu)

// ubuntuNameFilter matches the latest Ubuntu 26.04 LTS server image for arch.
// hvm-ssd* covers both the hvm-ssd and hvm-ssd-gp3 storage variants.
func ubuntuNameFilter(arch string) string {
	token := "amd64"
	if arch == "arm64" {
		token = "arm64"
	}
	return "ubuntu/images/hvm-ssd*/ubuntu-*-26.04-" + token + "-server-*"
}

// resolveArch collapses an instance type's SupportedArchitectures to one token,
// preferring x86_64 when both are present.
func resolveArch(supported []string) string {
	hasArm, hasX86 := false, false
	for _, a := range supported {
		switch a {
		case "arm64":
			hasArm = true
		case "x86_64":
			hasX86 = true
		}
	}
	if hasArm && !hasX86 {
		return "arm64"
	}
	return "x86_64"
}
