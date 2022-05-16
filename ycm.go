package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	coordv1 "k8s.io/api/coordination/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	coordclientset "k8s.io/client-go/kubernetes/typed/coordination/v1"
	listercoordv1 "k8s.io/client-go/listers/coordination/v1"
	"k8s.io/klog"
	"k8s.io/utils/clock"
	"k8s.io/utils/pointer"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	AnnotationKeyNodeAutonomy string = "node.beta.openyurt.io/autonomy" // nodeutil.AnnotationKeyNodeAutonomy
)

var (
	g_leaseClient coordclientset.LeaseInterface

	//leaseRenewalPeriod time.Duration = 180 * time.Second
	leaseRenewalPeriod time.Duration = 60 * time.Second
)

// Node is abstrcation of k8s Node
type Node struct {
	name       string
	autonomy   bool
	obj        *v1.Node
	statusTime metav1.Timestamp
	leaseTime  *metav1.MicroTime
	lease      *coordv1.Lease // lease related to this node
}

func nodeAutonomyAnnotated(node *v1.Node) bool {
	fmt.Printf("nodeAutonomyAnnotated: %v\n", node.Annotations[AnnotationKeyNodeAutonomy])
	if node.Annotations != nil && node.Annotations[AnnotationKeyNodeAutonomy] == "true" {
		fmt.Printf("autonomy annotation: %v\n", node.Annotations[AnnotationKeyNodeAutonomy])
		return true
	}
	return false
}

func (node *Node) updatedEnough() bool {
	if node.leaseTime == nil {
		return false
	}
	future := node.leaseTime.Add(leaseRenewalPeriod)
	now := metav1.NowMicro().Time
	if node.name == "ai-ice-vm05" {
		fmt.Printf("ai-ice-vm05:\n")
		fmt.Printf("leaseTime:\t%v\n", node.leaseTime)
		fmt.Printf("future:\t\t%v\n", future)
		fmt.Printf("now:\t\t%v\n", now)
	}
	if future.Before(now) {
		fmt.Printf("updateEnough: false\n")
		return false
	}
	fmt.Printf("updateEnough: true\n")
	return true
}

func (node *Node) alreadyTainted() bool {
	return true
}

func (node *Node) check() {
	if node.updatedEnough() {
		return
	}

	if node.isAutonomy() {
		if node.name == "ai-ice-vm05" {
			node.renewLease()
		}
	}
}

func (node *Node) isAutonomy() bool {
	return node.autonomy
}

func (node *Node) newLease() *coordv1.Lease {
	nl := &coordv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      node.name,
			Namespace: v1.NamespaceNodeLease,
		},
		Spec: coordv1.LeaseSpec{
			HolderIdentity:       pointer.StringPtr(node.name),
			LeaseDurationSeconds: pointer.Int32Ptr(40),
		},
	}
	return nl
}

func (node *Node) renewLease() {
	var nl *coordv1.Lease
	if node.lease != nil {
		nl = node.lease.DeepCopy()
	} else {
		nl = node.newLease()
	}
	nl.Spec.RenewTime = &metav1.MicroTime{Time: clock.RealClock{}.Now()}
	fmt.Printf("renew lease: %v\n", nl)
	lease, err := g_leaseClient.Update(context.Background(), nl, metav1.UpdateOptions{})
	if err != nil {
		fmt.Printf("renew lease error: %v\n", err)
		// renew lease error: leases.coordination.k8s.io "ai-ice-vm05" is invalid: metadata.resourceVersion: Invalid value: 0x0: must be specified for an update
	} else {
		fmt.Printf("renewed lease: %v\n", lease)
	}

}

func (node *Node) updateLease(lease *coordv1.Lease) {
	node.lease = lease
	node.leaseTime = lease.Spec.RenewTime
	fmt.Printf("node: %s, lease renew: %v\n", node.name, *node.leaseTime)
}

// add taint for those node marked as autonomy but status unoknown
func (node *Node) taint() {
	node.lease.Annotations["delegation"] = "true"
}

type NodePool struct {
	name     string
	autonomy bool
	nodes    []*Node
}

type NodeMap map[string]*Node

type NodePoolMap map[string]*NodePool

func (nm NodeMap) updateLease(lease *coordv1.Lease) {
	node := nm[lease.Name]
	if node == nil {
		fmt.Printf("no node %s yet\n", lease.Name)
		return
	}
	node.updateLease(lease)
}

var nodeMap NodeMap = make(map[string]*Node)
var nodepoolMap NodePoolMap = make(map[string]*NodePool)

func onNodeUpdate(o interface{}, n interface{}) {
	fmt.Println("node update:")
	oo := o.(*v1.Node)
	fmt.Println("<<<<<<<< old node:")
	fmt.Printf("%s\n", oo.Name)
	no := n.(*v1.Node)
	fmt.Println(">>>>>>>> new node:")
	fmt.Printf("%s\n", no.Name)
	nodeMap[oo.Name].obj = no
}

func onNodeAdd(o interface{}) {
	fmt.Println("node add:")
	oo := o.(*v1.Node)
	fmt.Printf("%s\n", oo.Name)
	fmt.Printf("%v\n", oo.Annotations)
	nodeMap[oo.Name] = &Node{
		name:     oo.Name,
		autonomy: nodeAutonomyAnnotated(oo),
		obj:      oo,
	}
	fmt.Printf("node added: %v\n", nodeMap[oo.Name])
}

func onNodeDelete(o interface{}) {
	fmt.Println("node delete:")
	oo := o.(*v1.Node)
	fmt.Printf("%s\n", oo.Name)
	delete(nodeMap, oo.Name)
}

func onLeaseUpdate(o interface{}, n interface{}) {
	//oo := o.(*coordv1.Lease)
	//fmt.Println("<<<<<<<< old lease:")
	no := n.(*coordv1.Lease)
	//fmt.Println(">>>>>>>> new lease:")
	//fmt.Printf("%v\n", no)

	/*
		fmt.Printf("Annotations: %v\n", no.Annotations)
		fmt.Printf("Spec.HolderIdentity: %v\n", no.Spec.HolderIdentity)
		fmt.Printf("Spec.AcquireTime: %v\n", no.Spec.AcquireTime)
		fmt.Printf("Spec.LeaseDurationSeconds: %d\n", *no.Spec.LeaseDurationSeconds) // 40
		fmt.Printf("Spec.LeaseTransitions: %v\n", no.Spec.LeaseTransitions)
		fmt.Printf("Spec.RenewTime: %v\n", no.Spec.RenewTime)
	*/

	nodeMap.updateLease(no)
}

func onLeaseAdd(o interface{}) {
	fmt.Println("lease add")
	oo := o.(*coordv1.Lease)
	nodeMap.updateLease(oo)
}

func onLeaseDelete(o interface{}) {
	oo := o.(*coordv1.Lease)
	fmt.Printf("lease deleted: %s\n", oo.Name)
}

func listLease(leaseLister listercoordv1.LeaseNamespaceLister) {
	for {
		selector := labels.Everything()
		leases, err := leaseLister.List(selector)
		if err != nil {
			panic(err.Error())
		}
		fmt.Printf("@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@ %v\n", leases)
	}
}

type controller struct {
	clientset         *kubernetes.Clientset
	nodes             NodeMap
	nodepools         NodePoolMap
	nodeMonitorPeriod time.Duration
}

func NewController(
	clientset *kubernetes.Clientset,
	nodeMonitorPeriod time.Duration,
	nodes NodeMap,
	nodepools NodePoolMap) *controller {
	return &controller{
		clientset:         clientset,
		nodes:             nodes,
		nodepools:         nodepools,
		nodeMonitorPeriod: nodeMonitorPeriod,
	}
}

// run every minute
func (nc *controller) monitorNodes() error {
	// get lease list
	for _, node := range nc.nodes {
		go node.check()
	}
	return nil
}

// update nodepool list
func (nc *controller) monitorNodepools() error {
	return nil
}

func (nc *controller) Run(stopCh <-chan struct{}) {
	go wait.Until(func() {
		if err := nc.monitorNodes(); err != nil {
			klog.Errorf("Error monitoring node health: %v", err)
		}
	}, nc.nodeMonitorPeriod, stopCh)

}

func main() {
	fmt.Println("nodepoolcoordination controller started.")

	//kubeconfig := flag.String("kubeconfig", "/home/nunu/.kube/config.aibox04", "absolute path to the kubeconfig file")
	kubeconfig := flag.String("kubeconfig", "/home/nunu/.kube/config.ai2-vm21", "absolute path to the kubeconfig file")
	flag.Parse()
	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		panic(err.Error())
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	g_leaseClient = clientset.CoordinationV1().Leases(v1.NamespaceNodeLease)

	stopper := make(chan struct{})
	defer close(stopper)

	sharedInformerFactory := informers.NewSharedInformerFactory(clientset, 0)
	go sharedInformerFactory.Start(stopper)

	nodeInformer := sharedInformerFactory.Core().V1().Nodes()
	nodeInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    onNodeAdd,
		UpdateFunc: onNodeUpdate,
		DeleteFunc: onNodeDelete,
	})

	filteredInformerFactory := informers.NewSharedInformerFactoryWithOptions(clientset, 0,
		informers.WithNamespace("kube-node-lease"),
		informers.WithTweakListOptions(func(*metav1.ListOptions) {}))
	go filteredInformerFactory.Start(stopper)
	leaseInformer := filteredInformerFactory.Coordination().V1().Leases()
	//leaseLister := leaseInformer.Lister().Leases("kube-node-lease")
	//go listLease(leaseLister)
	leaseInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    onLeaseAdd,
		UpdateFunc: onLeaseUpdate,
		DeleteFunc: onLeaseDelete,
	})
	//	leaseInformerSynced = leaseInformer.Informer().HasSynced

	nc := NewController(clientset, 10*time.Second, nodeMap, nodepoolMap)

	nc.Run(stopper)

	<-stopper
}
