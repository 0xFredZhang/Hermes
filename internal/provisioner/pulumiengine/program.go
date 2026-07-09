// Package pulumiengine builds Hermes blueprints into real AWS resources via the
// Pulumi Automation API. Named pulumiengine (not pulumi) to avoid clashing with
// the Pulumi SDK's own "pulumi" package.
package pulumiengine

import (
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/elasticache"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/rds"
	"github.com/pulumi/pulumi-random/sdk/v4/go/random"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
)

// buildProgram returns a Pulumi inline program declaring the blueprint's
// security group and EC2 instances in the account's default VPC.
func buildProgram(p provisioner.BlueprintParams) pulumi.RunFunc {
	return func(ctx *pulumi.Context) error {
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

		vpc, err := ec2.LookupVpc(ctx, &ec2.LookupVpcArgs{Default: pulumi.BoolRef(true)})
		if err != nil {
			return err
		}
		subnets, err := ec2.GetSubnets(ctx, &ec2.GetSubnetsArgs{
			Filters: []ec2.GetSubnetsFilter{{Name: "vpc-id", Values: []string{vpc.Id}}},
		})
		if err != nil {
			return err
		}
		if len(subnets.Ids) == 0 {
			return fmt.Errorf("no subnets found in default vpc %s", vpc.Id)
		}
		subnetID := subnets.Ids[0]
		subnetIDs := pulumi.StringArray{}
		for _, id := range subnets.Ids {
			subnetIDs = append(subnetIDs, pulumi.String(id))
		}

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
			VpcId:   pulumi.String(vpc.Id),
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
			if err := declareRDS(ctx, p.RDS, vpc.Id, subnetIDs, sg.ID().ToStringOutput()); err != nil {
				return err
			}
		}
		if p.Redis.Enabled {
			if err := declareRedis(ctx, p.Redis, vpc.Id, subnetIDs, sg.ID().ToStringOutput()); err != nil {
				return err
			}
		}

		var ids, ips, dns pulumi.StringArray
		for i := 0; i < p.EC2.Count; i++ {
			args := &ec2.InstanceArgs{
				Ami:                 pulumi.String(amiID),
				InstanceType:        pulumi.String(p.EC2.InstanceType),
				SubnetId:            pulumi.String(subnetID),
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
		return nil
	}
}

func declareRDS(ctx *pulumi.Context, cfg provisioner.RDS, vpcID string, subnetIDs pulumi.StringArray, appSG pulumi.StringOutput) error {
	dbSG, err := ec2.NewSecurityGroup(ctx, "hermes-rds-sg", &ec2.SecurityGroupArgs{
		VpcId: pulumi.String(vpcID),
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
	password, err := random.NewRandomPassword(ctx, "hermes-rds-password", &random.RandomPasswordArgs{
		Length:          pulumi.Int(24),
		Special:         pulumi.Bool(true),
		OverrideSpecial: pulumi.String("!#$%&*()-_=+[]{}<>:?"),
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
		Password:            password.Result,
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

func declareRedis(ctx *pulumi.Context, cfg provisioner.Redis, vpcID string, subnetIDs pulumi.StringArray, appSG pulumi.StringOutput) error {
	cacheSG, err := ec2.NewSecurityGroup(ctx, "hermes-redis-sg", &ec2.SecurityGroupArgs{
		VpcId: pulumi.String(vpcID),
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
	rg, err := elasticache.NewReplicationGroup(ctx, "hermes-redis", &elasticache.ReplicationGroupArgs{
		ApplyImmediately: pulumi.Bool(true),
		Description:      pulumi.String("Hermes Redis"),
		Engine:           pulumi.String(cfg.Engine),
		EngineVersion:    pulumi.String(cfg.EngineVersion),
		NodeType:         pulumi.String(cfg.NodeType),
		NumCacheClusters: pulumi.Int(cfg.NodeCount),
		Port:             pulumi.Int(cfg.Port),
		SecurityGroupIds: pulumi.StringArray{cacheSG.ID()},
		SubnetGroupName:  subnetGroup.Name,
	})
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
