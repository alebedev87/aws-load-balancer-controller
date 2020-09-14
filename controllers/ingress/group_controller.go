package ingress

import (
	"context"
	"github.com/go-logr/logr"
	networking "k8s.io/api/networking/v1beta1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/aws-alb-ingress-controller/controllers/ingress/eventhandlers"
	"sigs.k8s.io/aws-alb-ingress-controller/pkg/annotations"
	"sigs.k8s.io/aws-alb-ingress-controller/pkg/aws/services"
	"sigs.k8s.io/aws-alb-ingress-controller/pkg/deploy"
	"sigs.k8s.io/aws-alb-ingress-controller/pkg/ingress"
	networkingpkg "sigs.k8s.io/aws-alb-ingress-controller/pkg/networking"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const controllerName = "ingress"

// NewGroupReconciler constructs new GroupReconciler
func NewGroupReconciler(k8sClient client.Client, eventRecorder record.EventRecorder, ec2Client services.EC2, elbv2Client services.ELBV2, vpcID string, clusterName string,
	subnetsResolver networkingpkg.SubnetsResolver, logger logr.Logger) *GroupReconciler {
	annotationParser := annotations.NewSuffixAnnotationParser("alb.ingress.kubernetes.io")
	authConfigBuilder := ingress.NewDefaultAuthConfigBuilder(annotationParser)
	enhancedBackendBuilder := ingress.NewDefaultEnhancedBackendBuilder(annotationParser)
	modelBuilder := ingress.NewDefaultModelBuilder(k8sClient, eventRecorder, ec2Client, vpcID, clusterName, annotationParser, subnetsResolver,
		authConfigBuilder, enhancedBackendBuilder)
	stackMarshaller := deploy.NewDefaultStackMarshaller()
	stackDeployer := deploy.NewDefaultStackDeployer(k8sClient, elbv2Client, vpcID, clusterName, "ingress.k8s.aws", logger)
	groupLoader := ingress.NewDefaultGroupLoader(k8sClient, annotationParser, "alb")
	finalizerManager := ingress.NewDefaultFinalizerManager(k8sClient)

	return &GroupReconciler{
		groupLoader:      groupLoader,
		finalizerManager: finalizerManager,
		modelBuilder:     modelBuilder,
		stackMarshaller:  stackMarshaller,
		stackDeployer:    stackDeployer,
		log:              logger,
	}
}

// GroupReconciler reconciles a ingress group
type GroupReconciler struct {
	modelBuilder    ingress.ModelBuilder
	stackMarshaller deploy.StackMarshaller
	stackDeployer   deploy.StackDeployer

	groupLoader      ingress.GroupLoader
	finalizerManager ingress.FinalizerManager
	log              logr.Logger
}

// +kubebuilder:rbac:groups=networking.k8s.io,resources=Ingress,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=Ingress/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=extensions,resources=Ingress,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=extensions,resources=Ingress/status,verbs=get;update;patch
// Reconcile
func (r *GroupReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	groupID := ingress.DecodeGroupIDFromReconcileRequest(req)
	_ = r.log.WithValues("groupID", groupID)
	group, err := r.groupLoader.Load(ctx, groupID)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.finalizerManager.AddGroupFinalizer(ctx, groupID, group.Members...); err != nil {
		return ctrl.Result{}, err
	}

	stack, lb, err := r.modelBuilder.Build(ctx, group)
	if err != nil {
		return ctrl.Result{}, err
	}
	stackJSON, err := r.stackMarshaller.Marshal(stack)
	if err != nil {
		return ctrl.Result{}, err
	}
	r.log.Info("successfully built model", "model", stackJSON)
	if err := r.stackDeployer.Deploy(ctx, stack); err != nil {
		return ctrl.Result{}, err
	}
	lbARN, err := lb.LoadBalancerARN().Resolve(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}
	r.log.Info("successfully deployed model", "LB", lbARN)

	if err := r.finalizerManager.RemoveGroupFinalizer(ctx, groupID, group.InactiveMembers...); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *GroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	c, err := controller.New(controllerName, mgr, controller.Options{
		MaxConcurrentReconciles: 1,
		Reconciler:              r,
	})
	if err != nil {
		return err
	}
	return r.setupWatches(mgr, c, r.groupLoader)
}

func (r *GroupReconciler) setupWatches(mgr ctrl.Manager, c controller.Controller, groupLoader ingress.GroupLoader) error {
	ingEventHandler := eventhandlers.NewEnqueueRequestsForIngressEvent(groupLoader, mgr.GetEventRecorderFor(controllerName))
	if err := c.Watch(&source.Kind{Type: &networking.Ingress{}}, ingEventHandler); err != nil {
		return err
	}
	return nil
}
