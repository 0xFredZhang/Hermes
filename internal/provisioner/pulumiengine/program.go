// Package pulumiengine builds Hermes blueprints into real AWS resources via the
// Pulumi Automation API. Named pulumiengine (not pulumi) to avoid clashing with
// the Pulumi SDK's own "pulumi" package.
package pulumiengine

import (
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ssm"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/0xFredZhang/Hermes/internal/provisioner"
)

// al2023SSMParam resolves the latest Amazon Linux 2023 AMI per region.
const al2023SSMParam = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64"

// buildProgram returns a Pulumi inline program declaring the blueprint's
// security group and EC2 instances in the account's default VPC.
func buildProgram(p provisioner.BlueprintParams) pulumi.RunFunc {
	return func(ctx *pulumi.Context) error {
		amiID := p.EC2.AMI
		if amiID == "" {
			param, err := ssm.LookupParameter(ctx, &ssm.LookupParameterArgs{Name: al2023SSMParam})
			if err != nil {
				return err
			}
			amiID = param.Value
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
			ids = append(ids, inst.ID().ToStringOutput())
			ips = append(ips, inst.PublicIp)
			dns = append(dns, inst.PublicDns)
		}

		ctx.Export("instance_ids", ids)
		ctx.Export("public_ips", ips)
		ctx.Export("public_dns", dns)
		return nil
	}
}
