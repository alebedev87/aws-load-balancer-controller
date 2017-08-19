package albingresses

import (
	"sync"

	"github.com/aws/aws-sdk-go/service/elbv2"

	extensions "k8s.io/api/extensions/v1beta1"
	"k8s.io/client-go/tools/record"
	"k8s.io/ingress/core/pkg/ingress/annotations/class"

	"github.com/coreos/alb-ingress-controller/pkg/albingress"
	albelbv2 "github.com/coreos/alb-ingress-controller/pkg/aws/elbv2"
	"github.com/coreos/alb-ingress-controller/pkg/util/log"
	util "github.com/coreos/alb-ingress-controller/pkg/util/types"
)

// ALBIngresses is a list of ALBIngress. It is held by the ALBController instance and evaluated
// against to determine what should be created, deleted, and modified.
type ALBIngresses []*albingress.ALBIngress

var logger *log.Logger

func init() {
	logger = log.New("ingresses")
}

// NewALBIngressesFromIngressesOptions are the options to NewALBIngressesFromIngresses
type NewALBIngressesFromIngressesOptions struct {
	Recorder            record.EventRecorder
	ClusterName         string
	Ingresses           []interface{}
	ALBIngresses        ALBIngresses
	IngressClass        string
	DefaultIngressClass string
	GetServiceNodePort  func(string, int32) (*int64, error)
	GetNodes            func() util.AWSStringSlice
}

// NewALBIngressesFromIngresses returns a ALBIngresses created from the Kubernetes ingress state.
func NewALBIngressesFromIngresses(o *NewALBIngressesFromIngressesOptions) ALBIngresses {
	var ALBIngresses ALBIngresses

	// Find every ingress currently in Kubernetes.
	for _, k8singress := range o.Ingresses {
		ingResource := k8singress.(*extensions.Ingress)

		// Ensure the ingress resource found contains an appropriate ingress class.
		if !class.IsValid(ingResource, o.IngressClass, o.DefaultIngressClass) {
			continue
		}

		// Find the existing ingress for this Kubernetes ingress (if it existed).
		id := albingress.ID(ingResource.GetNamespace(), ingResource.Name)
		_, existingIngress := o.ALBIngresses.FindByID(id)

		// Produce a new ALBIngress instance for every ingress found. If ALBIngress returns nil, there
		// was an issue with the ingress (e.g. bad annotations) and should not be added to the list.
		ALBIngress := albingress.NewALBIngressFromIngress(&albingress.NewALBIngressFromIngressOptions{
			Ingress:            ingResource,
			ExistingIngress:    existingIngress,
			ClusterName:        o.ClusterName,
			GetServiceNodePort: o.GetServiceNodePort,
			GetNodes:           o.GetNodes,
			Recorder:           o.Recorder,
		})

		// Add the new ALBIngress instance to the new ALBIngress list.
		ALBIngresses = append(ALBIngresses, ALBIngress)
	}
	return ALBIngresses
}

// AssembleIngressesFromAWSOptions are the options to AssembleIngressesFromAWS
type AssembleIngressesFromAWSOptions struct {
	Recorder    record.EventRecorder
	ClusterName string
}

// AssembleIngressesFromAWS builds a list of existing ingresses from resources in AWS
func AssembleIngressesFromAWS(o *AssembleIngressesFromAWSOptions) ALBIngresses {
	logger.Infof("Build up list of existing ingresses")
	var ingresses ALBIngresses

	// Fetch a list of load balancers that match this cluser name
	loadBalancers, err := albelbv2.ELBV2svc.ClusterLoadBalancers(&o.ClusterName)
	if err != nil {
		logger.Fatalf(err.Error())
	}

	var wg sync.WaitGroup
	wg.Add(len(loadBalancers))

	// Generate the list of ingresses from those load balancers
	for _, loadBalancer := range loadBalancers {
		go func(wg *sync.WaitGroup, loadBalancer *elbv2.LoadBalancer) {
			defer wg.Done()

			albIngress, err := albingress.NewALBIngressFromAWSLoadBalancer(&albingress.NewALBIngressFromAWSLoadBalancerOptions{
				LoadBalancer: loadBalancer,
				ClusterName:  o.ClusterName,
				Recorder:     o.Recorder,
			})
			if err != nil {
				logger.Fatalf(err.Error())
			}

			ingresses = append(ingresses, albIngress)
		}(&wg, loadBalancer)
	}
	wg.Wait()

	logger.Infof("Assembled %d ingresses from existing AWS resources", len(ingresses))
	return ingresses
}

// Find locates the ingress with the same id as the ingress parameter provider and returns its position.
func (a ALBIngresses) Find(b *albingress.ALBIngress) int {
	for p, v := range a {
		if *v.Id == *b.Id {
			return p
		}
	}
	return -1
}

// FindByID locates the ingress by the id parameter and returns its position
func (a ALBIngresses) FindByID(id string) (int, *albingress.ALBIngress) {
	for p, v := range a {
		if *v.Id == id {
			return p, v
		}
	}
	return -1, nil
}

// RemovedIngresses compares the ingress list to the ingress list in the type, returning any ingresses that
// are not in the ingress list parameter.
func (a ALBIngresses) RemovedIngresses(newList ALBIngresses) ALBIngresses {
	var deleteableIngress ALBIngresses

	// Loop through every ingress in current (old) ingress list known to ALBController
	for _, ingress := range a {
		// Ingress objects not found in newList might qualify for deletion.
		if i := newList.Find(ingress); i < 0 {
			// If the ALBIngress still contains a LoadBalancer, it still needs to be deleted.
			// In this case, strip all desired state and add it to the deleteableIngress list.
			// If the ALBIngress contains no LoadBalancer, it was previously deleted and is
			// no longer relevant to the ALBController.
			if ingress.LoadBalancer != nil {
				ingress.StripDesiredState()
				deleteableIngress = append(deleteableIngress, ingress)
			}
		}
	}
	return deleteableIngress
}
