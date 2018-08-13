/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package targetgroup

import (
	"fmt"
	"strings"

	"github.com/kubernetes-sigs/aws-alb-ingress-controller/internal/ingress/controller/config"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/kubernetes-sigs/aws-alb-ingress-controller/internal/aws/albelbv2"
	"github.com/kubernetes-sigs/aws-alb-ingress-controller/internal/ingress/annotations/parser"
	"github.com/kubernetes-sigs/aws-alb-ingress-controller/internal/ingress/errors"
	"github.com/kubernetes-sigs/aws-alb-ingress-controller/internal/ingress/resolver"
	util "github.com/kubernetes-sigs/aws-alb-ingress-controller/pkg/util/types"
)

type Config struct {
	Attributes              albelbv2.TargetGroupAttributes
	BackendProtocol         *string
	HealthyThresholdCount   *int64
	SuccessCodes            *string
	TargetType              *string
	UnhealthyThresholdCount *int64
}

type targetGroup struct {
	r resolver.Resolver
}

const (
	DefaultBackendProtocol         = elbv2.ProtocolEnumHttp
	DefaultHealthyThresholdCount   = 2
	DefaultUnhealthyThresholdCount = 2
	DefaultSuccessCodes            = "200"
)

// NewParser creates a new target group annotation parser
func NewParser(r resolver.Resolver) parser.IngressAnnotation {
	return targetGroup{r}
}

// Parse parses the annotations contained in the resource
func (tg targetGroup) Parse(ing parser.AnnotationInterface) (interface{}, error) {
	cfg := tg.r.GetConfig()

	targetType, err := parser.GetStringAnnotation("target-type", ing)
	if err != nil {
		targetType = aws.String(cfg.DefaultTargetType)
	}

	if *targetType != elbv2.TargetTypeEnumInstance && *targetType != "pod" {
		// if *targetType != elbv2.TargetTypeEnumInstance && *targetType != elbv2.TargetTypeEnumIp {
		return "", errors.NewInvalidAnnotationContent("target-type", targetType)
	}

	backendProtocol, err := parser.GetStringAnnotation("backend-protocol", ing)
	if err != nil {
		backendProtocol = aws.String(DefaultBackendProtocol)
	}

	healthyThresholdCount, err := parser.GetInt64Annotation("healthy-threshold-count", ing)
	if err != nil {
		healthyThresholdCount = aws.Int64(DefaultHealthyThresholdCount)
	}

	unhealthyThresholdCount, err := parser.GetInt64Annotation("unhealthy-threshold-count", ing)
	if err != nil {
		unhealthyThresholdCount = aws.Int64(DefaultUnhealthyThresholdCount)
	}

	// support legacy successCodes annotation
	successCodes, err := parser.GetStringAnnotation("successCodes", ing)
	if err != nil {
		successCodes = aws.String(DefaultSuccessCodes)
	}

	s, err := parser.GetStringAnnotation("success-codes", ing)
	if err == nil {
		successCodes = s
	}

	var attributes albelbv2.TargetGroupAttributes

	tgAttr, err := parser.GetStringAnnotation("target-group-attributes", ing)
	if err == nil {
		var badAttrs []string
		rawAttrs := util.NewAWSStringSlice(*tgAttr)

		for _, rawAttr := range rawAttrs {
			parts := strings.Split(*rawAttr, "=")
			switch {
			case *rawAttr == "":
				continue
			case len(parts) != 2:
				badAttrs = append(badAttrs, *rawAttr)
				continue
			}
			attributes.Set(parts[0], parts[1])
		}

		if len(badAttrs) > 0 {
			return nil, fmt.Errorf("Unable to parse `%s` into Key=Value pair(s)", strings.Join(badAttrs, ", "))
		}
	}

	return &Config{
		TargetType:              targetType,
		BackendProtocol:         backendProtocol,
		HealthyThresholdCount:   healthyThresholdCount,
		UnhealthyThresholdCount: unhealthyThresholdCount,
		SuccessCodes:            successCodes,
		Attributes:              attributes,
	}, nil
}

func (a *Config) Merge(b *Config, cfg *config.Configuration) {
	a.TargetType = parser.MergeString(a.TargetType, b.TargetType, cfg.DefaultTargetType)
	a.BackendProtocol = parser.MergeString(a.BackendProtocol, b.BackendProtocol, DefaultBackendProtocol)
	a.HealthyThresholdCount = parser.MergeInt64(a.HealthyThresholdCount, b.HealthyThresholdCount, DefaultHealthyThresholdCount)
	a.UnhealthyThresholdCount = parser.MergeInt64(a.UnhealthyThresholdCount, b.UnhealthyThresholdCount, DefaultUnhealthyThresholdCount)
	a.SuccessCodes = parser.MergeString(a.SuccessCodes, b.SuccessCodes, DefaultSuccessCodes)

	if a.Attributes == nil {
		if b.Attributes != nil {
			a.Attributes = b.Attributes
		}
	}
}

func Dummy() *Config {
	return &Config{
		BackendProtocol:         aws.String(elbv2.ProtocolEnumHttp),
		HealthyThresholdCount:   aws.Int64(2),
		SuccessCodes:            aws.String("200"),
		TargetType:              aws.String(elbv2.TargetTypeEnumInstance),
		UnhealthyThresholdCount: aws.Int64(2),
	}
}
