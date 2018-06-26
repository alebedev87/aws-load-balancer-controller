package albingress

import (
	"sync"

	"github.com/kubernetes-sigs/aws-alb-ingress-controller/pkg/alb/lb"
	"github.com/kubernetes-sigs/aws-alb-ingress-controller/pkg/annotations"
	"github.com/kubernetes-sigs/aws-alb-ingress-controller/pkg/util/log"
	util "github.com/kubernetes-sigs/aws-alb-ingress-controller/pkg/util/types"
	extensions "k8s.io/api/extensions/v1beta1"
	"k8s.io/client-go/tools/record"
)

var logger *log.Logger

func init() {
	logger = log.New("ingress")
}

// ALBIngresses is a list of ALBIngress. It is held by the ALBController instance and evaluated
// against to determine what should be created, deleted, and modified.
type ALBIngresses []*ALBIngress

// ALBIngress contains all information above the cluster, ingress resource, and AWS resources
// needed to assemble an ALB, TargetGroup, Listener and Rules.
type ALBIngress struct {
	id                    string
	namespace             string
	ingressName           string
	clusterName           string
	albNamePrefix         string
	recorder              record.EventRecorder
	ingress               *extensions.Ingress
	lock                  *sync.Mutex
	annotations           *annotations.Annotations
	managedSecurityGroups util.AWSStringSlice // sgs managed by this controller rather than annotation
	loadBalancer          *lb.LoadBalancer
	valid                 bool
	logger                *log.Logger
	reconciled            bool
}

type ReconcileOptions struct {
	Eventf func(string, string, string, ...interface{})
}
