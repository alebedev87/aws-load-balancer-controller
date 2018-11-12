package sg

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go/aws/awsutil"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/kubernetes-sigs/aws-alb-ingress-controller/internal/albctx"
	"github.com/kubernetes-sigs/aws-alb-ingress-controller/internal/aws"
)

// SecurityGroup represents an SecurityGroup resource in AWS
type SecurityGroup struct {
	// We identify SecurityGroup either by GroupID or GroupName
	GroupID   *string
	GroupName *string

	InboundPermissions []*ec2.IpPermission
}

// SecurityGroupController manages SecurityGroups
type SecurityGroupController interface {
	// Reconcile ensures the securityGroup exists and match the specification.
	// Field GroupID or GroupName will be populated if unspecified.
	Reconcile(context.Context, *SecurityGroup) error

	// Delete ensures the securityGroup does not exist.
	Delete(context.Context, *SecurityGroup) error
}

type securityGroupController struct {
	cloud aws.CloudAPI
}

func (controller *securityGroupController) Reconcile(ctx context.Context, group *SecurityGroup) error {
	instance, err := controller.findExistingSGInstance(group)
	if err != nil {
		return err
	}
	if instance != nil {
		return controller.reconcileByModifySGInstance(ctx, group, instance)
	}
	return controller.reconcileByNewSGInstance(ctx, group)
}

func (controller *securityGroupController) Delete(ctx context.Context, group *SecurityGroup) error {
	if group.GroupID != nil {
		albctx.GetLogger(ctx).Infof("deleting securityGroup %s", aws.StringValue(group.GroupID))
		return controller.cloud.DeleteSecurityGroupByID(*group.GroupID)
	}
	instance, err := controller.findExistingSGInstance(group)
	if err != nil {
		return err
	}
	if instance != nil {
		albctx.GetLogger(ctx).Infof("deleting securityGroup %s", aws.StringValue(instance.GroupId))
		return controller.cloud.DeleteSecurityGroupByID(*instance.GroupId)
	}
	return nil
}

func (controller *securityGroupController) reconcileByNewSGInstance(ctx context.Context, group *SecurityGroup) error {
	createSGOutput, err := controller.cloud.CreateSecurityGroupWithContext(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   group.GroupName,
		Description: aws.String("Instance SecurityGroup created by alb-ingress-controller"),
	})
	if err != nil {
		return err
	}
	group.GroupID = createSGOutput.GroupId

	_, err = controller.cloud.AuthorizeSecurityGroupIngressWithContext(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId:       group.GroupID,
		IpPermissions: group.InboundPermissions,
	})
	if err != nil {
		return err
	}

	_, err = controller.cloud.CreateTagsWithContext(ctx, &ec2.CreateTagsInput{
		Resources: []*string{group.GroupID},
		Tags: []*ec2.Tag{
			{
				Key:   aws.String("Name"),
				Value: group.GroupName,
			},
			{
				Key:   aws.String(aws.ManagedByKey),
				Value: aws.String(aws.ManagedByValue),
			},
		},
	})
	if err != nil {
		return err
	}
	albctx.GetLogger(ctx).Infof("created new securityGroup %s", aws.StringValue(group.GroupID))

	return nil
}

// reconcileByModifySGInstance modified the sg instance in AWS to match the specification specified in group
func (controller *securityGroupController) reconcileByModifySGInstance(ctx context.Context, group *SecurityGroup, instance *ec2.SecurityGroup) error {
	if group.GroupID == nil {
		group.GroupID = instance.GroupId
	}
	if group.GroupName == nil {
		group.GroupName = instance.GroupName
	}

	permissionsToRevoke := diffIPPermissions(instance.IpPermissions, group.InboundPermissions)
	if len(permissionsToRevoke) != 0 {
		albctx.GetLogger(ctx).Infof("revoking inbound permissions from securityGroup %s: %v", aws.StringValue(group.GroupID), awsutil.Prettify(permissionsToRevoke))
		_, err := controller.cloud.RevokeSecurityGroupIngressWithContext(ctx, &ec2.RevokeSecurityGroupIngressInput{
			GroupId:       group.GroupID,
			IpPermissions: permissionsToRevoke,
		})
		if err != nil {
			return fmt.Errorf("failed to revoke inbound permissions due to %v", err)
		}
	}

	permissionsToGrant := diffIPPermissions(group.InboundPermissions, instance.IpPermissions)
	if len(permissionsToGrant) != 0 {
		albctx.GetLogger(ctx).Infof("granting inbound permissions to securityGroup %s: %v", aws.StringValue(group.GroupID), awsutil.Prettify(permissionsToGrant))
		_, err := controller.cloud.AuthorizeSecurityGroupIngressWithContext(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
			GroupId:       group.GroupID,
			IpPermissions: permissionsToGrant,
		})
		if err != nil {
			return fmt.Errorf("failed to grant inbound permissions due to %v", err)
		}
	}
	return nil
}

// findExistingSGInstance tring to find the existing SG matches the specification
func (controller *securityGroupController) findExistingSGInstance(group *SecurityGroup) (*ec2.SecurityGroup, error) {
	switch {
	case group.GroupID != nil:
		{
			instance, err := controller.cloud.GetSecurityGroupByID(aws.StringValue(group.GroupID))
			if err != nil {
				return nil, err
			}
			if instance == nil {
				return nil, fmt.Errorf("securityGroup %s doesn't exist", aws.StringValue(group.GroupID))
			}
			return instance, nil
		}
	case group.GroupName != nil:
		{
			instance, err := controller.cloud.GetSecurityGroupByName(aws.StringValue(group.GroupName))
			if err != nil {
				return nil, err
			}
			return instance, nil
		}
	}
	return nil, fmt.Errorf("Either GroupID or GroupName must be specified")
}

// diffIPPermissions calcutes set_difference as source - target
func diffIPPermissions(source []*ec2.IpPermission, target []*ec2.IpPermission) (diffs []*ec2.IpPermission) {
	for _, sPermission := range source {
		containsInTarget := false
		for _, tPermission := range target {
			if ipPermissionEquals(sPermission, tPermission) {
				containsInTarget = true
				break
			}
		}
		if !containsInTarget {
			diffs = append(diffs, sPermission)
		}
	}
	return diffs
}

// ipPermissionEquals test whether two IPPermission instance are equals
func ipPermissionEquals(source *ec2.IpPermission, target *ec2.IpPermission) bool {
	if aws.StringValue(source.IpProtocol) != aws.StringValue(target.IpProtocol) {
		return false
	}
	if aws.Int64Value(source.FromPort) != aws.Int64Value(target.FromPort) {
		return false
	}
	if aws.Int64Value(source.ToPort) != aws.Int64Value(target.ToPort) {
		return false
	}
	if len(diffIPRanges(source.IpRanges, target.IpRanges)) != 0 {
		return false
	}
	if len(diffIPRanges(target.IpRanges, source.IpRanges)) != 0 {
		return false
	}
	if len(diffUserIDGroupPairs(source.UserIdGroupPairs, target.UserIdGroupPairs)) != 0 {
		return false
	}
	if len(diffUserIDGroupPairs(target.UserIdGroupPairs, source.UserIdGroupPairs)) != 0 {
		return false
	}

	return true
}

// diffIPRanges calcutes set_difference as source - target
func diffIPRanges(source []*ec2.IpRange, target []*ec2.IpRange) (diffs []*ec2.IpRange) {
	for _, sRange := range source {
		containsInTarget := false
		for _, tRange := range target {
			if ipRangeEquals(sRange, tRange) {
				containsInTarget = true
				break
			}
		}
		if !containsInTarget {
			diffs = append(diffs, sRange)
		}
	}
	return diffs
}

// ipRangeEquals test whether two IPRange instance are equals
func ipRangeEquals(source *ec2.IpRange, target *ec2.IpRange) bool {
	return aws.StringValue(source.CidrIp) == aws.StringValue(target.CidrIp)
}

// diffUserIDGroupPairs calculates set_difference as source - target
func diffUserIDGroupPairs(source []*ec2.UserIdGroupPair, target []*ec2.UserIdGroupPair) (diffs []*ec2.UserIdGroupPair) {
	for _, sPair := range source {
		containsInTarget := false
		for _, tPair := range target {
			if userIDGroupPairEquals(sPair, tPair) {
				containsInTarget = true
				break
			}
		}
		if !containsInTarget {
			diffs = append(diffs, sPair)
		}
	}
	return diffs
}

// userIDGroupPairEquals test whether two UserIdGroupPair equals
// currently we only check for groupId
func userIDGroupPairEquals(source *ec2.UserIdGroupPair, target *ec2.UserIdGroupPair) bool {
	return aws.StringValue(source.GroupId) == aws.StringValue(target.GroupId)
}
