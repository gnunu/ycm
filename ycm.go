package main

import (
	"flag"
	"fmt"

	coordv1 "k8s.io/api/coordination/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	listercoordv1 "k8s.io/client-go/listers/coordination/v1"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

// Node is abstrcation of k8s Node
type Node struct {
	name       string
	autonomy   bool
	obj        *v1.Node
	statusTime metav1.Timestamp
	leaseTime  metav1.Timestamp
	lease      *coordv1.Lease // lease related to this node
}

func newNodeWithObj(obj *v1.Node) *Node {
	return &Node{
		name:     obj.Name,
		autonomy: false,
		obj:      obj,
	}
}

func newNodeWithName(name string) *Node {
	return &Node{
		name:     name,
		autonomy: false,
	}
}

func (node *Node) updatedEnough() bool {
	return true
}

func (node *Node) alreadyTainted() bool {
	return true
}

func (node *Node) check() {
	if node.updatedEnough() {
		return
	}

	if node.labeledAutonomy() {
		if node.alreadyTainted() {
			return
		}
		node.taint()
	}
}

func (node *Node) labeledAutonomy() bool {
	return true
}

func (node *Node) updateLease(lease *coordv1.Lease) {
	node.lease = lease
}

// add taint for those node marked as autonomy but status unoknown
func (node *Node) taint() {
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
	node.lease = lease
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
	nodeMap[oo.Name] = &Node{
		name:     oo.Name,
		autonomy: false,
		obj:      oo,
	}
	fmt.Printf("nodeMap=%v\n", nodeMap)
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
	//fmt.Printf("%v\n", oo)
	no := n.(*coordv1.Lease)
	fmt.Printf("lease update: %s\n", no.Name)
	//fmt.Println(">>>>>>>> new lease:")
	//fmt.Printf("%v\n", no)
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
	nodes     NodeMap
	nodepools NodePoolMap
}

func newController() *controller {
	return &controller{
		nodes:     nodeMap,
		nodepools: nodepoolMap,
	}
}

// run every minute
func (nc *controller) MointorNodes() {
	// get lease list
	for _, node := range nc.nodes {
		go node.check()
	}
}

// update nodepool list
func (nc *controller) MonitorNodepools() {

}

func main() {
	fmt.Println("nodepoolcoordination controller started.")

	kubeconfig := flag.String("kubeconfig", "/home/nunu/.kube/config.aibox04", "absolute path to the kubeconfig file")
	flag.Parse()
	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		panic(err.Error())
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

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

	//nc := newController()

	/*	go wait.UntilWithContext(ctx, func(ctx context.Context) {
			if err := nc.monitorNodeHealth(ctx); err != nil {
				klog.Errorf("Error monitoring node health: %v", err)
			}
		}, nc.nodeMonitorPeriod)
	*/

	<-stopper
}
