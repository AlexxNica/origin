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

package networking

import (
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/unversioned"
	"k8s.io/kubernetes/pkg/apis/extensions"
	"k8s.io/kubernetes/pkg/util/intstr"
	"k8s.io/kubernetes/test/e2e/framework"

	"fmt"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

/* Taken from kubernetes PR #35660 and locally modified:
   1. Update "package" decl, wrap all tests in InNetworkPolicyContext().
   2. Ported to use unversioned API (since we haven't yet rebased to include
      kube #36673).
   3. Ported to use IPs rather than DNS names in tests since our extended
      networking tests don't run with DNS.
*/

/*
The following Network Policy tests verify that policy object definitions
are correctly enforced by a networking plugin. It accomplishes this by launching
a simple netcat server, and two clients with different
attributes. Each test case creates a network policy which should only allow
connections from one of the clients. The test then asserts that the clients
failed or succesfully connected as expected.
*/

var _ = Describe("NetworkPolicy", func() {
	InNetworkPolicyContext(func() {
		f := framework.NewDefaultFramework("network-policy")

		It("should support setting DefaultDeny namespace policy [Feature:NetworkPolicy]", func() {
			ns := f.Namespace

			By("Create a simple server.")
			podServer, service := createServerPodAndService(f, ns, "server", []int{80})
			defer func() {
				By("Cleaning up the server.")
				if err := f.ClientSet.Core().Pods(ns.Name).Delete(podServer.Name, nil); err != nil {
					framework.Failf("unable to cleanup pod %v: %v", podServer.Name, err)
				}
			}()
			defer func() {
				By("Cleaning up the server's service.")
				if err := f.ClientSet.Core().Services(ns.Name).Delete(service.Name, nil); err != nil {
					framework.Failf("unable to cleanup svc %v: %v", service.Name, err)
				}
			}()
			framework.Logf("Waiting for Server to come up.")
			err := framework.WaitForPodRunningInNamespace(f.ClientSet, podServer)
			Expect(err).NotTo(HaveOccurred())

			// Create a pod with name 'client-a', which should be able to communicate with server.
			By("Creating client which will be able to contact the server since isolation is off.")
			testCanConnect(f, ns, "client-can-connect", service, 80)

			framework.Logf("Enabling network isolation.")
			setNamespaceIsolation(f, ns, "DefaultDeny")

			// Create a pod with name 'client-b', which will attempt to comunicate with the server,
			// but should not be able to now that isolation is on.
			testCannotConnect(f, ns, "client-cannot-connect", service, 80)
		})

		It("should enforce policy based on PodSelector [Feature:NetworkPolicy]", func() {
			ns := f.Namespace
			setNamespaceIsolation(f, ns, "DefaultDeny")

			By("Creating a simple server.")
			serverPod, service := createServerPodAndService(f, ns, "server", []int{80})
			defer func() {
				By("Cleaning up the server.")
				if err := f.ClientSet.Core().Pods(ns.Name).Delete(serverPod.Name, nil); err != nil {
					framework.Failf("unable to cleanup pod %v: %v", serverPod.Name, err)
				}
			}()
			defer func() {
				By("Cleaning up the server's service.")
				if err := f.ClientSet.Core().Services(ns.Name).Delete(service.Name, nil); err != nil {
					framework.Failf("unable to cleanup svc %v: %v", service.Name, err)
				}
			}()
			framework.Logf("Waiting for Server to come up.")
			err := framework.WaitForPodRunningInNamespace(f.ClientSet, serverPod)
			Expect(err).NotTo(HaveOccurred())

			By("Creating a network policy for the server which allows traffic from the pod 'client-a'.")

			policy := extensions.NetworkPolicy{
				ObjectMeta: api.ObjectMeta{
					Name: "allow-client-a-via-pod-selector",
				},
				Spec: extensions.NetworkPolicySpec{
					// Apply this policy to the Server
					PodSelector: unversioned.LabelSelector{
						MatchLabels: map[string]string{
							"pod-name": serverPod.Name,
						},
					},
					// Allow traffic only from client-a
					Ingress: []extensions.NetworkPolicyIngressRule{{
						From: []extensions.NetworkPolicyPeer{{
							PodSelector: &unversioned.LabelSelector{
								MatchLabels: map[string]string{
									"pod-name": "client-a",
								},
							},
						}},
					}},
				},
			}

			_, err = f.ClientSet.Extensions().NetworkPolicies(ns.Name).Create(&policy)

			Expect(err).NotTo(HaveOccurred())
			defer func() {
				By("Cleaning up the policy.")
				if err = f.ClientSet.Extensions().NetworkPolicies(ns.Name).Delete(policy.Name, nil); err != nil {
					framework.Failf("unable to cleanup policy %v: %v", policy.Name, err)
				}
			}()

			By("Creating client-a which should be able to contact the server.")
			testCanConnect(f, ns, "client-a", service, 80)
			testCannotConnect(f, ns, "client-b", service, 80)
		})

		It("should enforce policy based on Ports [Feature:NetworkPolicy]", func() {
			ns := f.Namespace

			// Create Server with Service
			By("Creating a simple server.")
			serverPod, service := createServerPodAndService(f, ns, "server", []int{80, 81})
			defer func() {
				By("Cleaning up the server.")
				if err := f.ClientSet.Core().Pods(ns.Name).Delete(serverPod.Name, nil); err != nil {
					framework.Failf("unable to cleanup pod %v: %v", serverPod.Name, err)
				}
			}()
			defer func() {
				By("Cleaning up the server's service.")
				if err := f.ClientSet.Core().Services(ns.Name).Delete(service.Name, nil); err != nil {
					framework.Failf("unable to cleanup svc %v: %v", service.Name, err)
				}
			}()
			framework.Logf("Waiting for Server to come up.")
			err := framework.WaitForPodRunningInNamespace(f.ClientSet, serverPod)
			Expect(err).NotTo(HaveOccurred())

			By("Testing pods can connect to both ports when isolation is off.")
			testCanConnect(f, ns, "basecase-reachable-80", service, 80)
			testCanConnect(f, ns, "basecase-reachable-81", service, 81)

			setNamespaceIsolation(f, ns, "DefaultDeny")

			By("Testing pods cannot by default when isolation is turned on.")
			testCannotConnect(f, ns, "basecase-unreachable-80", service, 80)
			testCannotConnect(f, ns, "basecase-unreachable-81", service, 81)

			By("Creating a network policy for the Service which allows traffic only to one port.")
			policy := extensions.NetworkPolicy{
				ObjectMeta: api.ObjectMeta{
					Name: "allow-ingress-on-port-81",
				},
				Spec: extensions.NetworkPolicySpec{
					// Apply to server
					PodSelector: unversioned.LabelSelector{
						MatchLabels: map[string]string{
							"pod-name": serverPod.Name,
						},
					},
					// Allow traffic only to one port.
					Ingress: []extensions.NetworkPolicyIngressRule{{
						Ports: []extensions.NetworkPolicyPort{{
							Port: &intstr.IntOrString{IntVal: 81},
						}},
					}},
				},
			}
			_, err = f.ClientSet.Extensions().NetworkPolicies(ns.Name).Create(&policy)

			Expect(err).NotTo(HaveOccurred())
			defer func() {
				By("Cleaning up the policy.")
				if err = f.ClientSet.Extensions().NetworkPolicies(ns.Name).Delete(policy.Name, nil); err != nil {
					framework.Failf("unable to cleanup policy %v: %v", policy.Name, err)
				}
			}()

			testCannotConnect(f, ns, "client-a", service, 80)
			testCanConnect(f, ns, "client-b", service, 81)
		})

		It("shouldn't enforce policy when isolation is off [Feature:NetworkPolicy]", func() {
			ns := f.Namespace

			// Create Server with Service
			By("Creating a simple server.")
			serverPod, service := createServerPodAndService(f, ns, "server", []int{80, 81})
			defer func() {
				By("Cleaning up the server.")
				if err := f.ClientSet.Core().Pods(ns.Name).Delete(serverPod.Name, nil); err != nil {
					framework.Failf("unable to cleanup pod %v: %v", serverPod.Name, err)
				}
			}()
			defer func() {
				By("Cleaning up the server's service.")
				if err := f.ClientSet.Core().Services(ns.Name).Delete(service.Name, nil); err != nil {
					framework.Failf("unable to cleanup svc %v: %v", service.Name, err)
				}
			}()
			framework.Logf("Waiting for Server to come up.")
			err := framework.WaitForPodRunningInNamespace(f.ClientSet, serverPod)
			Expect(err).NotTo(HaveOccurred())

			By("Testing pods can connect to both ports when isolation is off and no policy is defined.")
			testCanConnect(f, ns, "basecase-reachable-a", service, 80)
			testCanConnect(f, ns, "basecase-reachable-b", service, 81)

			By("Creating a network policy for the Service which allows traffic only to one port.")
			policy := extensions.NetworkPolicy{
				ObjectMeta: api.ObjectMeta{
					Name: "allow-ingress-on-port-81",
				},
				Spec: extensions.NetworkPolicySpec{
					// Apply to server
					PodSelector: unversioned.LabelSelector{
						MatchLabels: map[string]string{
							"pod-name": serverPod.Name,
						},
					},
					// Allow traffic only to one port.
					Ingress: []extensions.NetworkPolicyIngressRule{{
						Ports: []extensions.NetworkPolicyPort{{
							Port: &intstr.IntOrString{IntVal: 81},
						}},
					}},
				},
			}
			_, err = f.ClientSet.Extensions().NetworkPolicies(ns.Name).Create(&policy)

			Expect(err).NotTo(HaveOccurred())
			defer func() {
				By("Cleaning up the policy.")
				if err = f.ClientSet.Extensions().NetworkPolicies(ns.Name).Delete(policy.Name, nil); err != nil {
					framework.Failf("unable to cleanup policy %v: %v", policy.Name, err)
				}
			}()

			testCanConnect(f, ns, "client-a", service, 80)
			testCanConnect(f, ns, "client-b", service, 81)
		})

		It("should enforce multiple, stacked policies with overlapping podSelectors [Feature:NetworkPolicy]", func() {
			ns := f.Namespace

			// Create Server with Service
			By("Creating a simple server.")
			serverPod, service := createServerPodAndService(f, ns, "server", []int{80, 81})
			defer func() {
				By("Cleaning up the server.")
				if err := f.ClientSet.Core().Pods(ns.Name).Delete(serverPod.Name, nil); err != nil {
					framework.Failf("unable to cleanup pod %v: %v", serverPod.Name, err)
				}
			}()
			defer func() {
				By("Cleaning up the server's service.")
				if err := f.ClientSet.Core().Services(ns.Name).Delete(service.Name, nil); err != nil {
					framework.Failf("unable to cleanup svc %v: %v", service.Name, err)
				}
			}()
			framework.Logf("Waiting for Server to come up.")
			err := framework.WaitForPodRunningInNamespace(f.ClientSet, serverPod)
			Expect(err).NotTo(HaveOccurred())

			By("Testing pods can connect to both ports when isolation is off.")
			testCanConnect(f, ns, "test-a", service, 80)
			testCanConnect(f, ns, "test-b", service, 81)

			setNamespaceIsolation(f, ns, "DefaultDeny")

			By("Testing pods cannot connect to either port when no policy is defined.")
			testCannotConnect(f, ns, "test-a-2", service, 80)
			testCannotConnect(f, ns, "test-b-2", service, 81)

			By("Creating a network policy for the Service which allows traffic only to one port.")
			policy := extensions.NetworkPolicy{
				ObjectMeta: api.ObjectMeta{
					Name: "allow-ingress-on-port-80",
				},
				Spec: extensions.NetworkPolicySpec{
					// Apply to server
					PodSelector: unversioned.LabelSelector{
						MatchLabels: map[string]string{
							"pod-name": serverPod.Name,
						},
					},
					// Allow traffic only to one port.
					Ingress: []extensions.NetworkPolicyIngressRule{{
						Ports: []extensions.NetworkPolicyPort{{
							Port: &intstr.IntOrString{IntVal: 80},
						}},
					}},
				},
			}
			_, err = f.ClientSet.Extensions().NetworkPolicies(ns.Name).Create(&policy)

			Expect(err).NotTo(HaveOccurred())
			defer func() {
				By("Cleaning up the policy.")
				if err = f.ClientSet.Extensions().NetworkPolicies(ns.Name).Delete(policy.Name, nil); err != nil {
					framework.Failf("unable to cleanup policy %v: %v", policy.Name, err)
				}
			}()

			By("Creating a network policy for the Service which allows traffic only to another port.")
			policy2 := extensions.NetworkPolicy{
				ObjectMeta: api.ObjectMeta{
					Name: "allow-ingress-on-port-81",
				},
				Spec: extensions.NetworkPolicySpec{
					// Apply to server
					PodSelector: unversioned.LabelSelector{
						MatchLabels: map[string]string{
							"pod-name": serverPod.Name,
						},
					},
					// Allow traffic only to one port.
					Ingress: []extensions.NetworkPolicyIngressRule{{
						Ports: []extensions.NetworkPolicyPort{{
							Port: &intstr.IntOrString{IntVal: 81},
						}},
					}},
				},
			}
			_, err = f.ClientSet.Extensions().NetworkPolicies(ns.Name).Create(&policy2)

			Expect(err).NotTo(HaveOccurred())
			defer func() {
				By("Cleaning up the policy.")
				if err = f.ClientSet.Extensions().NetworkPolicies(ns.Name).Delete(policy2.Name, nil); err != nil {
					framework.Failf("unable to cleanup policy %v: %v", policy2.Name, err)
				}
			}()

			testCanConnect(f, ns, "client-a", service, 80)
			testCanConnect(f, ns, "client-b", service, 81)
		})

		It("should support allow-all policy [Feature:NetworkPolicy]", func() {
			ns := f.Namespace

			// Create Server with Service
			By("Creating a simple server.")
			serverPod, service := createServerPodAndService(f, ns, "server", []int{80, 81})
			defer func() {
				By("Cleaning up the server.")
				if err := f.ClientSet.Core().Pods(ns.Name).Delete(serverPod.Name, nil); err != nil {
					framework.Failf("unable to cleanup pod %v: %v", serverPod.Name, err)
				}
			}()
			defer func() {
				By("Cleaning up the server's service.")
				if err := f.ClientSet.Core().Services(ns.Name).Delete(service.Name, nil); err != nil {
					framework.Failf("unable to cleanup svc %v: %v", service.Name, err)
				}
			}()
			framework.Logf("Waiting for Server to come up.")
			err := framework.WaitForPodRunningInNamespace(f.ClientSet, serverPod)
			Expect(err).NotTo(HaveOccurred())

			By("Testing pods can connect to both ports when isolation is off.")
			testCanConnect(f, ns, "test-a", service, 80)
			testCanConnect(f, ns, "test-b", service, 81)

			setNamespaceIsolation(f, ns, "DefaultDeny")

			By("Testing pods cannot connect to either port when isolation is on.")
			testCannotConnect(f, ns, "test-a", service, 80)
			testCannotConnect(f, ns, "test-b", service, 81)

			By("Creating a network policy which allows all traffic.")
			policy := extensions.NetworkPolicy{
				ObjectMeta: api.ObjectMeta{
					Name: "allow-all",
				},
				Spec: extensions.NetworkPolicySpec{
					// Allow all traffic
					PodSelector: unversioned.LabelSelector{
						MatchLabels: map[string]string{},
					},
					Ingress: []extensions.NetworkPolicyIngressRule{{}},
				},
			}
			_, err = f.ClientSet.Extensions().NetworkPolicies(ns.Name).Create(&policy)

			Expect(err).NotTo(HaveOccurred())
			defer func() {
				By("Cleaning up the policy.")
				if err = f.ClientSet.Extensions().NetworkPolicies(ns.Name).Delete(policy.Name, nil); err != nil {
					framework.Failf("unable to cleanup policy %v: %v", policy.Name, err)
				}
			}()

			testCanConnect(f, ns, "client-a", service, 80)
			testCanConnect(f, ns, "client-b", service, 81)
		})

		It("should enforce policy based on NamespaceSelector [Feature:NetworkPolicy]", func() {
			nsA := f.Namespace
			nsBName := f.BaseName + "-b"
			// The CreateNamespace helper uses the input name as a Name Generator, so the namespace itself
			// will have a different name than what we are setting as the value of ns-name.
			// This is fine as long as we don't try to match the label as nsB.Name in our policy.
			nsB, err := f.CreateNamespace(nsBName, map[string]string{
				"ns-name": nsBName,
			})
			Expect(err).NotTo(HaveOccurred())
			setNamespaceIsolation(f, nsA, "DefaultDeny")

			// Create Server with Service in NS-B
			By("Creating a webserver tied to a service.")
			serverPod, service := createServerPodAndService(f, nsA, "server", []int{80})
			defer func() {
				By("Cleaning up the server.")
				if err := f.ClientSet.Core().Pods(nsA.Name).Delete(serverPod.Name, nil); err != nil {
					framework.Failf("unable to cleanup pod %v: %v", serverPod.Name, err)
				}
			}()
			defer func() {
				By("Cleaning up the server's service.")
				if err := f.ClientSet.Core().Services(nsA.Name).Delete(service.Name, nil); err != nil {
					framework.Failf("unable to cleanup svc %v: %v", service.Name, err)
				}
			}()
			framework.Logf("Waiting for server to come up.")
			err = framework.WaitForPodRunningInNamespace(f.ClientSet, serverPod)
			Expect(err).NotTo(HaveOccurred())

			// Create Policy for that service that allows traffic only via namespace B
			By("Creating a network policy for the server which allows traffic from namespace-b.")
			policy := extensions.NetworkPolicy{
				ObjectMeta: api.ObjectMeta{
					Name: "allow-ns-b-via-namespace-selector",
				},
				Spec: extensions.NetworkPolicySpec{
					// Apply to server
					PodSelector: unversioned.LabelSelector{
						MatchLabels: map[string]string{
							"pod-name": serverPod.Name,
						},
					},
					// Allow traffic only from NS-B
					Ingress: []extensions.NetworkPolicyIngressRule{{
						From: []extensions.NetworkPolicyPeer{{
							NamespaceSelector: &unversioned.LabelSelector{
								MatchLabels: map[string]string{
									"ns-name": nsBName,
								},
							},
						}},
					}},
				},
			}
			_, err = f.ClientSet.Extensions().NetworkPolicies(nsA.Name).Create(&policy)

			Expect(err).NotTo(HaveOccurred())
			defer func() {
				By("Cleaning up the policy.")
				if err = f.ClientSet.Extensions().NetworkPolicies(nsA.Name).Delete(policy.Name, nil); err != nil {
					framework.Failf("unable to cleanup policy %v: %v", policy.Name, err)
				}
			}()

			testCannotConnect(f, nsA, "client-a", service, 80)
			testCanConnect(f, nsB, "client-b", service, 80)
		})
	})
})

func testCanConnect(f *framework.Framework, ns *api.Namespace, podName string, service *api.Service, targetPort int) {
	By(fmt.Sprintf("Creating client pod %s that should successfully connect to %s.", podName, service.Name))
	podClient := createNetworkClientPod(f, ns, podName, service.Spec.ClusterIP, targetPort)
	defer func() {
		By(fmt.Sprintf("Cleaning up the pod %s", podName))
		if err := f.ClientSet.Core().Pods(ns.Name).Delete(podClient.Name, nil); err != nil {
			framework.Failf("unable to cleanup pod %v: %v", podClient.Name, err)
		}
	}()

	framework.Logf("Waiting for %s to complete.", podClient.Name)
	err := framework.WaitForPodNoLongerRunningInNamespace(f.ClientSet, podClient.Name, ns.Name, "0")
	Expect(err).NotTo(HaveOccurred(), "Pod did not finish as expected.")

	framework.Logf("Waiting for %s to complete.", podClient.Name)
	err = framework.WaitForPodSuccessInNamespace(f.ClientSet, podClient.Name, ns.Name)
	Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("checking %s could communicate with server.", podClient.Name))
}

func testCannotConnect(f *framework.Framework, ns *api.Namespace, podName string, service *api.Service, targetPort int) {
	By(fmt.Sprintf("Creating client pod %s that should not be able to connect to %s.", podName, service.Name))
	podClient := createNetworkClientPod(f, ns, podName, service.Spec.ClusterIP, targetPort)
	defer func() {
		By(fmt.Sprintf("Cleaning up the pod %s", podName))
		if err := f.ClientSet.Core().Pods(ns.Name).Delete(podClient.Name, nil); err != nil {
			framework.Failf("unable to cleanup pod %v: %v", podClient.Name, err)
		}
	}()

	framework.Logf("Waiting for %s to complete.", podClient.Name)
	err := framework.WaitForPodSuccessInNamespace(f.ClientSet, podClient.Name, ns.Name)
	Expect(err).To(HaveOccurred(), fmt.Sprintf("checking %s could not communicate with server.", podName))
}

// Create a server pod with a listening container for each port in ports[].
// Will also assign a pod label with key: "pod-name" and label set to the given podname for later use by the network
// policy.
func createServerPodAndService(f *framework.Framework, namespace *api.Namespace, podName string, ports []int) (*api.Pod, *api.Service) {
	// Because we have a variable amount of ports, we'll first loop through and generate our Containers for our pod,
	// and ServicePorts.for our Service.
	containers := []api.Container{}
	servicePorts := []api.ServicePort{}
	for _, port := range ports {
		// Build the containers for the server pod.
		containers = append(containers, api.Container{
			Name:  fmt.Sprintf("%s-container-%d", podName, port),
			Image: "gcr.io/google_containers/redis:e2e",
			Args: []string{
				"/bin/sh",
				"-c",
				fmt.Sprintf("/bin/nc -kl %d", port),
			},
			Ports: []api.ContainerPort{{ContainerPort: int32(port)}},
		})

		// Build the Service Ports for the service.
		servicePorts = append(servicePorts, api.ServicePort{
			Name:       fmt.Sprintf("%s-%d", podName, port),
			Port:       int32(port),
			TargetPort: intstr.FromInt(port),
		})
	}

	By(fmt.Sprintf("Creating a server pod %s in namespace %s", podName, namespace.Name))
	pod, err := f.ClientSet.Core().Pods(namespace.Name).Create(&api.Pod{
		ObjectMeta: api.ObjectMeta{
			Name: podName,
			Labels: map[string]string{
				"pod-name": podName,
			},
		},
		Spec: api.PodSpec{
			Containers:    containers,
			RestartPolicy: api.RestartPolicyNever,
		},
	})
	Expect(err).NotTo(HaveOccurred())
	framework.Logf("Created pod %v", pod.ObjectMeta.Name)

	svcName := fmt.Sprintf("svc-%s", podName)
	By(fmt.Sprintf("Creating a service %s for pod %s in namespace %s", svcName, podName, namespace.Name))
	svc, err := f.ClientSet.Core().Services(namespace.Name).Create(&api.Service{
		ObjectMeta: api.ObjectMeta{
			Name: svcName,
		},
		Spec: api.ServiceSpec{
			Ports: servicePorts,
			Selector: map[string]string{
				"pod-name": podName,
			},
		},
	})
	Expect(err).NotTo(HaveOccurred())
	framework.Logf("Created service %s", svc.Name)

	return pod, svc
}

// Create a client pod which will attempt a netcat to the provided service, on the specified port.
// This client will attempt a oneshot connection, then die, without restarting the pod.
// Test can then be asserted based on whether the pod quit with an error or not.
func createNetworkClientPod(f *framework.Framework, namespace *api.Namespace, podName string, targetIP string, targetPort int) *api.Pod {
	pod, err := f.ClientSet.Core().Pods(namespace.Name).Create(&api.Pod{
		ObjectMeta: api.ObjectMeta{
			Name: podName,
			Labels: map[string]string{
				"pod-name": podName,
			},
		},
		Spec: api.PodSpec{
			RestartPolicy: api.RestartPolicyNever,
			Containers: []api.Container{
				{
					Name:  fmt.Sprintf("%s-container", podName),
					Image: "gcr.io/google_containers/redis:e2e",
					Args: []string{
						"/bin/sh",
						"-c",
						fmt.Sprintf("/usr/bin/printf dummy-data | /bin/nc -w 8 %s %d", targetIP, targetPort),
					},
				},
			},
		},
	})

	Expect(err).NotTo(HaveOccurred())
	return pod
}

// Configure namespace network isolation by setting the network-policy annotation
// on the namespace.
func setNamespaceIsolation(f *framework.Framework, namespace *api.Namespace, ingressIsolation string) {
	var annotations = map[string]string{}
	if ingressIsolation != "" {
		By(fmt.Sprintf("Enabling isolation through namespace annotations on namespace %v", namespace.Name))
		policy := fmt.Sprintf(`{"ingress":{"isolation":"%s"}}`, ingressIsolation)
		annotations["net.beta.kubernetes.io/network-policy"] = policy
	} else {
		By(fmt.Sprintf("Disabling isolation through namespace annotations on namespace %v", namespace.Name))
		delete(annotations, "net.beta.kubernetes.io/network-policy")
	}

	// Update the namespace.  We set the resource version to be an empty
	// string, this forces the update.  If we weren't to do this, we would
	// either need to re-query the namespace, or update the namespace
	// references with the one returned by the update.  This approach
	// requires less plumbing.
	namespace.ObjectMeta.Annotations = annotations
	namespace.ObjectMeta.ResourceVersion = ""
	_, err := f.ClientSet.Core().Namespaces().Update(namespace)
	Expect(err).NotTo(HaveOccurred())
}
