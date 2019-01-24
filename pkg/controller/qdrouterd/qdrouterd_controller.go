package qdrouterd

import (
	"bytes"
	"context"
	"reflect"
	"strconv"
	"text/template"

	v1alpha1 "github.com/ajssmith/qdrouterd-operator/pkg/apis/ajssmith/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const certRequestAnnotation = "service.alpha.ajssmith.io/serving-cert-secret-name"

var log = logf.Log.WithName("controller_qdrouterd")

// Add creates a new Qdrouterd Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileQdrouterd{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("qdrouterd-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource Qdrouterd
	err = c.Watch(&source.Kind{Type: &v1alpha1.Qdrouterd{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// Watch for changes to secondary resource Deployment and requeue the owner Qdrouterd
	err = c.Watch(&source.Kind{Type: &appsv1.Deployment{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &v1alpha1.Qdrouterd{},
	})
	if err != nil {
		return err
	}

	// Watch for changes to secondary resource Service and requeue the owner Qdrouterd
	err = c.Watch(&source.Kind{Type: &corev1.Service{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &v1alpha1.Qdrouterd{},
	})
	if err != nil {
		return err
	}

	return nil
}

var _ reconcile.Reconciler = &ReconcileQdrouterd{}

// ReconcileQdrouterd reconciles a Qdrouterd object
type ReconcileQdrouterd struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a Qdrouterd object and makes changes based on the state read
// and what is in the Qdrouterd.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileQdrouterd) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling Qdrouterd")

	// Fetch the Qdrouterd instance
	instance := &v1alpha1.Qdrouterd{}
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	requestCert := setQdrouterdDefaults(instance)

	// Check if the deployment already exists, if not create a new one
	found := &appsv1.Deployment{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: instance.Name, Namespace: instance.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		// Define a new deployment
		dep := r.newDeploymentForCR(instance)
		reqLogger.Info("Creating a new Deployment %s%s\n", dep.Namespace, dep.Name)
		err = r.client.Create(context.TODO(), dep)
		if err != nil {
			reqLogger.Info("Failed to create new Deployment: %v\n", err)
			return reconcile.Result{}, err
		}
		// Deployment created successfully - return and requeue
        controllerutil.SetControllerReference(instance, dep, r.scheme)
		return reconcile.Result{Requeue: true}, nil
	} else if err != nil {
		reqLogger.Info("Failed to get Deployment: %v\n", err)
		return reconcile.Result{}, err
	}

	// Ensure the deployment count is the same as the spec size
	count := instance.Spec.Count
	if *found.Spec.Replicas != count {
		found.Spec.Replicas = &count
		err = r.client.Update(context.TODO(), found)
		if err != nil {
			reqLogger.Info("Failed to update Deployment: %v\n", err)
			return reconcile.Result{}, err
		}
		// Spec updated - return and requeue
		reqLogger.Info("Deployment count updated")
		return reconcile.Result{Requeue: true}, nil
	}

    // TODO(ansmith): until the qdrouterd can re-read config, this can wait
	// Ensure the deployment container matches the instance spec

	// Check if the service for the deployment already exists, if not create a new one
	locate := &corev1.Service{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: instance.Name, Namespace: instance.Namespace}, locate)
	if err != nil && errors.IsNotFound(err) {
		// Define a new service
		svc := r.newServiceForCR(instance, requestCert)
		reqLogger.Info("Creating service for qdrouterd deployment")
		err = r.client.Create(context.TODO(), svc)
		if err != nil {
			reqLogger.Info("Failed to create new Service: %v\n", err)
			return reconcile.Result{}, err
		}
		// Service created successfully - return and requeue
        controllerutil.SetControllerReference(instance, svc, r.scheme) 
		return reconcile.Result{Requeue: true}, nil
	} else if err != nil {
		reqLogger.Info("Failed to get Service: %v\n", err)
		return reconcile.Result{}, err
	}

	// List the pods for this deployment
	podList := &corev1.PodList{}
	labelSelector := labels.SelectorFromSet(labelsForQdrouterd(instance.Name))
	listOps := &client.ListOptions{Namespace: instance.Namespace, LabelSelector: labelSelector}
	err = r.client.List(context.TODO(), listOps, podList)
	if err != nil {
		reqLogger.Info("Failed to list pods: %v\n", err)
		return reconcile.Result{}, err
	}
	podNames := getPodNames(podList.Items)

	// Update status.PodNames if needed
	if !reflect.DeepEqual(podNames, instance.Status.PodNames) {
		instance.Status.PodNames = podNames
		err := r.client.Update(context.TODO(), instance)
		if err != nil {
			reqLogger.Info("Failed to update pod names: %v\n", err)
			return reconcile.Result{}, err
		}
		reqLogger.Info("Pod names updated")
		return reconcile.Result{Requeue: true}, nil
	}

	return reconcile.Result{}, nil
}

func isDefaultSslProfileDefined(m *v1alpha1.Qdrouterd) bool {
	for _, profile := range m.Spec.SslProfiles {
		if profile.Name == "default" {
			return true
		}
	}
	return false
}

func isDefaultSslProfileUsed(m *v1alpha1.Qdrouterd) bool {
	for _, listener := range m.Spec.Listeners {
		if listener.SslProfile == "default" {
			return true
		}
	}
	for _, listener := range m.Spec.InterRouterListeners {
		if listener.SslProfile == "default" {
			return true
		}
	}
	return false
}

func setQdrouterdDefaults(m *v1alpha1.Qdrouterd) bool {
	requestCert := false
	if len(m.Spec.Listeners) == 0 {
		m.Spec.Listeners = append(m.Spec.Listeners, v1alpha1.Listener{
			Port: 5672,
		}, v1alpha1.Listener{
			Port:       5671,
			SslProfile: "default",
		}, v1alpha1.Listener{
			Port:       8672,
			Http:       true,
			SslProfile: "default",
		})
	}
	if len(m.Spec.InterRouterListeners) == 0 {
		m.Spec.InterRouterListeners = append(m.Spec.InterRouterListeners, v1alpha1.Listener{
			Port: 55672,
		})
	}
	if !isDefaultSslProfileDefined(m) && isDefaultSslProfileUsed(m) {
		m.Spec.SslProfiles = append(m.Spec.SslProfiles, v1alpha1.SslProfile{
			Name:        "default",
			Credentials: m.Name + "-cert",
		})
		requestCert = true
	}
	for _, profile := range m.Spec.SslProfiles {
		if profile.Credentials == "" {
			profile.Credentials = m.Name + "-cert"
			requestCert = true
		}
	}
	return requestCert
}

func configForQdrouterd(m *v1alpha1.Qdrouterd) string {
	config := `
    router {
        mode: interior
        id: Router.${HOSTNAME_IP_ADDRESS}
    }
    
    {{range .Listeners}}
    listener {
        {{- if .Name}}
        name: {{.Name}}
        {{- end}}
        {{- if .Host}}
        host: {{.Host}}
        {{- else}}
        host: 0.0.0.0
        {{- end}}
        {{- if .Port}}
        port: {{.Port}}
        {{- end}}
        {{- if .RouteContainer}}
        role: route-container
        {{- else}}
        role: normal
        {{- end}}
        {{- if .Http}}
        http: true
        httpRootDir: /usr/share/qpid-dispatch/console
        {{- end}}
        {{- if .SslProfile}}
        sslProfile: {{.SslProfile}}
        {{- end}}
    }
    {{- end}}
    
    {{range .InterRouterListeners}}
    listener {
        {{- if .Name}}
        name: {{.Name}}
        {{- end}}
        role: inter-router
        {{- if .Host}}
        host: {{.Host}}
        {{- else}}
        host: 0.0.0.0
        {{- end}}
        {{- if .Port}}
        port: {{.Port}}
        {{- end}}
        {{- if .Cost}}
        cost: {{.Cost}}
        {{- end}}
        {{- if .SslProfile}}
        sslProfile: {{.SslProfile}}
        {{- end}}
    }
    {{- end}}

    {{range .SslProfiles}}
    sslProfile {
       name: {{.Name}}
       {{- if .Credentials}}
       certFile: /etc/qpid-dispatch-certs/{{.Name}}/{{.Credentials}}/tls.crt
       privateKeyFile: /etc/qpid-dispatch-certs/{{.Name}}/{{.Credentials}}/tls.key
       {{- end}}
       {{- if .CaCert}}
       caCertFile: /etc/qpid-dispatch-certs/{{.Name}}/{{.CaCert}}/ca.crt
       {{- else if .RequireClientCerts}}
       caCertFile: /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
       {{- end}}
    }
    {{- end}}
    
    {{range .Addresses}}
    address {
        {{- if .Prefix}}
        prefix: {{.Prefix}}
        {{- end}}
        {{- if .Pattern}}
        pattern: {{.Pattern}}
        {{- end}}
        {{- if .Distribution}}
        distribution: {{.Distribution}}
        {{- end}}
        {{- if .Waypoint}}
        waypoint: {{.Waypoint}}
        {{- end}}
        {{- if .IngressPhase}}
        ingressPhase: {{.IngressPhase}}
        {{- end}}
        {{- if .EgressPhase}}
        egressPhase: {{.EgressPhase}}
        {{- end}}
    }
    {{- end}}

    {{range .AutoLinks}}
    autoLink {
        {{- if .Address}}
        addr: {{.Address}}
        {{- end}}
        {{- if .Direction}}
        direction: {{.Direction}}
        {{- end}}
        {{- if .ContainerId}}
        containerId: {{.ContainerId}}
        {{- end}}
        {{- if .Connection}}
        connection: {{.Connection}}
        {{- end}}
        {{- if .ExternalPrefix}}
        externalPrefix: {{.ExternalPrefix}}
        {{- end}}
        {{- if .Phase}}
        Phase: {{.Phase}}
        {{- end}}
    }
    {{- end}}

    {{range .LinkRoutes}}
    linkRoute {
        {{- if .Prefix}}
        prefix: {{.Prefix}}
        {{- end}}
        {{- if .Pattern}}
        pattern: {{.Pattern}}
        {{- end}}
        {{- if .Direction}}
        direction: {{.Direction}}
        {{- end}}
        {{- if .Connection}}
        connection: {{.Connection}}
        {{- end}}
        {{- if .ContainerId}}
        containerId: {{.ContainerId}}
        {{- end}}
        {{- if .AddExternalPrefix}}
        addExternalPrefix: {{.AddExternalPrefix}}
        {{- end}}
        {{- if .RemoveExternalPrefix}}
        removeExternalPrefix: {{.RemoveExternalPrefix}}
        {{- end}}
    }
    {{- end}}

    {{range .Connectors}}
    connector {
        {{- if .Name}}
        name: {{.Name}}
        {{- end}}
        {{- if .Host}}
        host: {{.Host}}
        {{- end}}
        {{- if .Port}}
        port: {{.Port}}
        {{- end}}
        {{- if .RouteContainer}}
        routeContainer: {{.RouteContainer}}
        {{- end}}
        {{- if .Cost}}
        cost: {{.Cost}}
        {{- end}}
        {{- if .SslProfile}}
        sslProfile: {{.SslProfile}}
        {{- end}}
    }
    {{- end}}`

	var buff bytes.Buffer
	qdrouterdconfig := template.Must(template.New("qdrouterdconfig").Parse(config))
	qdrouterdconfig.Execute(&buff, m.Spec)
	return buff.String()
}

func checkQdrouterdContainer(desired *corev1.Container, actual *corev1.Container) bool {
	if desired.Image != actual.Image {
	    return false
	}
	if !reflect.DeepEqual(desired.Env, actual.Env) {
		return false
	}
	if !reflect.DeepEqual(desired.Ports, actual.Ports) {
		return false
	}
	if !reflect.DeepEqual(desired.VolumeMounts, actual.VolumeMounts) {
		return false
	}
	return true
}

func containerForQdrouterd(m *v1alpha1.Qdrouterd, config string) corev1.Container {
	container := corev1.Container{
		Image: m.Spec.Image,
		Name:  m.Name,
		Env: []corev1.EnvVar{
			{
				Name:  "QDROUTERD_CONF",
				Value: config,
			},
			{
				Name:  "QDROUTERD_AUTO_MESH_DISCOVERY",
				Value: "QUERY",
			},
			{
				Name:  "APPLICATION_NAME",
				Value: m.Name,
			},
			{
				Name: "POD_NAMESPACE",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "metadata.namespace",
					},
				},
			},
			{
				Name: "POD_IP",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "status.podIP",
					},
				},
			},
		},
		Ports: containerPortsForQdrouterd(m),
	}
	if m.Spec.SslProfiles != nil && len(m.Spec.SslProfiles)  > 0 {
		volumeMounts := []corev1.VolumeMount{}
		for _, profile := range m.Spec.SslProfiles {
			if len(profile.Credentials) > 0 {
				volumeMounts = append(volumeMounts, corev1.VolumeMount{
					Name: profile.Credentials,
					MountPath: "/etc/qpid-dispatch-certs/" + profile.Name + "/" + profile.Credentials,
				})
			}
			if len(profile.CaCert) > 0 && profile.CaCert != profile.Credentials {
				volumeMounts = append(volumeMounts, corev1.VolumeMount{
					Name: profile.CaCert,
					MountPath: "/etc/qpid-dispatch-certs/" + profile.Name + "/" + profile.CaCert,
				})
			}

		}
		container.VolumeMounts = volumeMounts
	}
	return container
}

func nameForListener(l *v1alpha1.Listener) string {
	if l.Name == "" {
		return "port-" + strconv.Itoa(int(l.Port))
	} else {
		return l.Name
	}
}

func containerPortsForListeners(listeners []v1alpha1.Listener) []corev1.ContainerPort {
	ports := []corev1.ContainerPort{}
	for _, listener := range listeners {
		ports = append(ports, corev1.ContainerPort{
			Name:          nameForListener(&listener),
			ContainerPort: listener.Port,
		})
	}
	return ports
}

func containerPortsForQdrouterd(m *v1alpha1.Qdrouterd) []corev1.ContainerPort {
	ports := containerPortsForListeners(m.Spec.Listeners)
	ports = append(ports, containerPortsForListeners(m.Spec.InterRouterListeners)...)
	return ports
}

func servicePortsForListeners(listeners []v1alpha1.Listener) []corev1.ServicePort {
	ports := []corev1.ServicePort{}
	for _, listener := range listeners {
		ports = append(ports, corev1.ServicePort{
			Name:       nameForListener(&listener),
			Protocol:   "TCP",
			Port:       listener.Port,
			TargetPort: intstr.FromInt(int(listener.Port)),
		})
	}
	return ports
}

func portsForQdrouterd(m *v1alpha1.Qdrouterd) []corev1.ServicePort {
	ports := []corev1.ServicePort{}
	external := servicePortsForListeners(m.Spec.Listeners)
	internal := servicePortsForListeners(m.Spec.InterRouterListeners)
	ports = append(ports, external...)
	ports = append(ports, internal...)
	return ports
}

// Create newDeploymentForCR method to create deployment
func (r *ReconcileQdrouterd) newDeploymentForCR(m *v1alpha1.Qdrouterd) *appsv1.Deployment {
	labels := labelsForQdrouterd(m.Name)
	config := configForQdrouterd(m)
	replicas := m.Spec.Count
	dep := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name,
			Namespace: m.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{containerForQdrouterd(m, config)},
				},
			},
		},
	}
	volumes := []corev1.Volume{}
	for _, profile := range m.Spec.SslProfiles {
		if len(profile.Credentials) > 0 {
			volumes = append(volumes, corev1.Volume{
				Name: profile.Credentials,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: profile.Credentials,
					},
				},
			})
		}
		if len(profile.CaCert) > 0 && profile.CaCert != profile.Credentials {
			volumes = append(volumes, corev1.Volume{
				Name: profile.CaCert,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: profile.CaCert,
					},
				},
			})
		}
	}
	dep.Spec.Template.Spec.Volumes = volumes

	return dep
}

func checkService(desired *corev1.Service, actual *corev1.Service) bool {
	update := false
	if !reflect.DeepEqual(desired.Annotations[certRequestAnnotation], actual.Annotations[certRequestAnnotation]) {
		actual.Annotations[certRequestAnnotation] = desired.Annotations[certRequestAnnotation]
	}
	if !reflect.DeepEqual(desired.Spec.Selector, actual.Spec.Selector) {
		actual.Spec.Selector = desired.Spec.Selector
	}
	if !reflect.DeepEqual(desired.Spec.Ports, actual.Spec.Ports) {
		actual.Spec.Ports = desired.Spec.Ports
	}
	return update
}

// Create newServiceForCR method to create service
func (r *ReconcileQdrouterd) newServiceForCR(m *v1alpha1.Qdrouterd, requestCert bool) *corev1.Service {
	labels := labelsForQdrouterd(m.Name)
	service := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name,
			Namespace: m.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports:    portsForQdrouterd(m),
		},
	}
	if requestCert {
		service.Annotations = map[string]string{certRequestAnnotation: m.Name + "-cert"}
	}
	// Set Qdrouterd instance as the owner and controller
	// controllerutil.SetControllerReference(m, service, r.scheme)
	return service
}

// Set labels in a map
func labelsForQdrouterd(name string) map[string]string {
	return map[string]string{"application": name, "qdrouterd_cr": name}
}

// getPodNames returns the pod names of the array of pods passed in
func getPodNames(pods []corev1.Pod) []string {
	var podNames []string
	for _, pod := range pods {
		podNames = append(podNames, pod.Name)
	}
	return podNames
}
