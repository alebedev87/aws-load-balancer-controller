package ls

import (
	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/kubernetes-sigs/aws-alb-ingress-controller/internal/alb/rs"
	"github.com/kubernetes-sigs/aws-alb-ingress-controller/internal/alb/tg"
	"github.com/kubernetes-sigs/aws-alb-ingress-controller/internal/ingress/controller/store"
	"github.com/kubernetes-sigs/aws-alb-ingress-controller/pkg/util/log"
	extensions "k8s.io/api/extensions/v1beta1"
)

// Listeners is a slice of Listener pointers
type Listeners []*Listener

// Listener contains the relevant ID, Rules, and current/desired Listeners
type Listener struct {
	ls             ls
	rules          rs.Rules
	defaultBackend *extensions.IngressBackend
	deleted        bool
	logger         *log.Logger
}

type ls struct {
	current *elbv2.Listener
	desired *elbv2.Listener
}

type ReconcileOptions struct {
	Store           store.Storer
	Ingress         *extensions.Ingress
	Eventf          func(string, string, string, ...interface{})
	LoadBalancerArn *string
	TargetGroups    tg.TargetGroups
}
