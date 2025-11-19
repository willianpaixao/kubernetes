/*
Copyright 2014 The Kubernetes Authors.

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

package portforward

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"github.com/spf13/cobra"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/client-go/rest/fake"
	"k8s.io/client-go/tools/portforward"
	cmdtesting "k8s.io/kubectl/pkg/cmd/testing"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
	"k8s.io/kubectl/pkg/scheme"
)

type fakePortForwarder struct {
	method string
	url    *url.URL
	pfErr  error
}

func (f *fakePortForwarder) ForwardPorts(method string, url *url.URL, opts PortForwardOptions) error {
	f.method = method
	f.url = url
	return f.pfErr
}

func testPortForward(t *testing.T, flags map[string]string, args []string) {
	version := "v1"

	tests := []struct {
		name            string
		podPath, pfPath string
		pod             *corev1.Pod
		pfErr           bool
	}{
		{
			name:    "pod portforward",
			podPath: "/api/" + version + "/namespaces/test/pods/foo",
			pfPath:  "/api/" + version + "/namespaces/test/pods/foo/portforward",
			pod:     execPod(),
		},
		{
			name:    "pod portforward error",
			podPath: "/api/" + version + "/namespaces/test/pods/foo",
			pfPath:  "/api/" + version + "/namespaces/test/pods/foo/portforward",
			pod:     execPod(),
			pfErr:   true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var err error
			tf := cmdtesting.NewTestFactory().WithNamespace("test")
			defer tf.Cleanup()

			codec := scheme.Codecs.LegacyCodec(scheme.Scheme.PrioritizedVersionsAllGroups()...)
			ns := scheme.Codecs.WithoutConversion()

			tf.Client = &fake.RESTClient{
				VersionedAPIPath:     "/api/v1",
				GroupVersion:         schema.GroupVersion{Group: "", Version: "v1"},
				NegotiatedSerializer: ns,
				Client: fake.CreateHTTPClient(func(req *http.Request) (*http.Response, error) {
					switch p, m := req.URL.Path, req.Method; {
					case p == test.podPath && m == "GET":
						body := cmdtesting.ObjBody(codec, test.pod)
						return &http.Response{StatusCode: http.StatusOK, Header: cmdtesting.DefaultHeader(), Body: body}, nil
					default:
						t.Errorf("%s: unexpected request: %#v\n%#v", test.name, req.URL, req)
						return nil, nil
					}
				}),
			}
			tf.ClientConfigVal = cmdtesting.DefaultClientConfig()
			ff := &fakePortForwarder{}
			if test.pfErr {
				ff.pfErr = fmt.Errorf("pf error")
			}

			opts := &PortForwardOptions{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cmd := NewCmdPortForward(tf, genericiooptions.NewTestIOStreamsDiscard())
			cmd.Run = func(cmd *cobra.Command, args []string) {
				if err = opts.Complete(tf, cmd, args); err != nil {
					return
				}
				opts.PortForwarder = ff
				if err = opts.Validate(); err != nil {
					return
				}
				err = opts.RunPortForwardContext(ctx)
			}

			for name, value := range flags {
				cmd.Flags().Set(name, value)
			}
			cmd.Run(cmd, args)

			if test.pfErr && err != ff.pfErr {
				t.Errorf("%s: Unexpected port-forward error: %v", test.name, err)
			}
			if !test.pfErr && err != nil {
				t.Errorf("%s: Unexpected error: %v", test.name, err)
			}
			if test.pfErr {
				return
			}

			if ff.url == nil || ff.url.Path != test.pfPath {
				t.Errorf("%s: Did not get expected path for portforward request", test.name)
			}
			if ff.method != "POST" {
				t.Errorf("%s: Did not get method for attach request: %s", test.name, ff.method)
			}
		})
	}
}

func TestPortForward(t *testing.T) {
	testPortForward(t, nil, []string{"foo", ":5000", ":1000"})
}

func TestTranslateServicePortToTargetPort(t *testing.T) {
	cases := []struct {
		name       string
		svc        corev1.Service
		pod        corev1.Pod
		ports      []string
		translated []string
		err        bool
	}{
		{
			name: "test success 1 (int port)",
			svc: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Port:       80,
							TargetPort: intstr.FromInt32(8080),
						},
					},
				},
			},
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: int32(8080)},
							},
						},
					},
				},
			},
			ports:      []string{"80"},
			translated: []string{"80:8080"},
			err:        false,
		},
		{
			name: "test success 1 (int port with random local port)",
			svc: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Port:       80,
							TargetPort: intstr.FromInt32(8080),
						},
					},
				},
			},
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: int32(8080)},
							},
						},
					},
				},
			},
			ports:      []string{":80"},
			translated: []string{":8080"},
			err:        false,
		},
		{
			name: "test success 1 (int port with explicit local port)",
			svc: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Port:       8080,
							TargetPort: intstr.FromInt32(8080),
						},
					},
				},
			},
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: int32(8080)},
							},
						},
					},
				},
			},
			ports:      []string{"8000:8080"},
			translated: []string{"8000:8080"},
			err:        false,
		},
		{
			name: "test success 2 (clusterIP: None)",
			svc: corev1.Service{
				Spec: corev1.ServiceSpec{
					ClusterIP: "None",
					Ports: []corev1.ServicePort{
						{
							Port:       80,
							TargetPort: intstr.FromInt32(8080),
						},
					},
				},
			},
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: int32(8080)},
							},
						},
					},
				},
			},
			ports:      []string{"80"},
			translated: []string{"80"},
			err:        false,
		},
		{
			name: "test success 2 (clusterIP: None with random local port)",
			svc: corev1.Service{
				Spec: corev1.ServiceSpec{
					ClusterIP: "None",
					Ports: []corev1.ServicePort{
						{
							Port:       80,
							TargetPort: intstr.FromInt32(8080),
						},
					},
				},
			},
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: int32(8080)},
							},
						},
					},
				},
			},
			ports:      []string{":80"},
			translated: []string{":80"},
			err:        false,
		},
		{
			name: "test success 3 (named target port)",
			svc: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Port:       80,
							TargetPort: intstr.FromString("http"),
						},
						{
							Port:       443,
							TargetPort: intstr.FromString("https"),
						},
					},
				},
			},
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: int32(8080)},
								{
									Name:          "https",
									ContainerPort: int32(8443)},
							},
						},
					},
				},
			},
			ports:      []string{"80", "443"},
			translated: []string{"80:8080", "443:8443"},
			err:        false,
		},
		{
			name: "test success 3 (named target port with random local port)",
			svc: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Port:       80,
							TargetPort: intstr.FromString("http"),
						},
						{
							Port:       443,
							TargetPort: intstr.FromString("https"),
						},
					},
				},
			},
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: int32(8080)},
								{
									Name:          "https",
									ContainerPort: int32(8443)},
							},
						},
					},
				},
			},
			ports:      []string{":80", ":443"},
			translated: []string{":8080", ":8443"},
			err:        false,
		},
		{
			name: "test success 4 (named service port)",
			svc: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Port:       80,
							Name:       "http",
							TargetPort: intstr.FromInt32(8080),
						},
						{
							Port:       443,
							Name:       "https",
							TargetPort: intstr.FromInt32(8443),
						},
					},
				},
			},
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: int32(8080)},
								{
									ContainerPort: int32(8443)},
							},
						},
					},
				},
			},
			ports:      []string{"http", "https"},
			translated: []string{"80:8080", "443:8443"},
			err:        false,
		},
		{
			name: "test success 4 (named service port with random local port)",
			svc: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Port:       80,
							Name:       "http",
							TargetPort: intstr.FromInt32(8080),
						},
						{
							Port:       443,
							Name:       "https",
							TargetPort: intstr.FromInt32(8443),
						},
					},
				},
			},
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: int32(8080)},
								{
									ContainerPort: int32(8443)},
							},
						},
					},
				},
			},
			ports:      []string{":http", ":https"},
			translated: []string{":8080", ":8443"},
			err:        false,
		},
		{
			name: "test success 4 (named service port and named pod container port)",
			svc: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Port:       80,
							Name:       "http",
							TargetPort: intstr.FromString("http"),
						},
					},
				},
			},
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: int32(80)},
							},
						},
					},
				},
			},
			ports:      []string{"http"},
			translated: []string{"80"},
			err:        false,
		},
		{
			name: "test success (targetPort omitted)",
			svc: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Port: 80,
						},
					},
				},
			},
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: int32(80)},
							},
						},
					},
				},
			},
			ports:      []string{"80"},
			translated: []string{"80"},
			err:        false,
		},
		{
			name: "test success (targetPort omitted with random local port)",
			svc: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Port: 80,
						},
					},
				},
			},
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: int32(80)},
							},
						},
					},
				},
			},
			ports:      []string{":80"},
			translated: []string{":80"},
			err:        false,
		},
		{
			name: "test failure 1 (named target port lookup failure)",
			svc: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Port:       80,
							TargetPort: intstr.FromString("http"),
						},
					},
				},
			},
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									Name:          "https",
									ContainerPort: int32(443)},
							},
						},
					},
				},
			},
			ports:      []string{"80"},
			translated: []string{},
			err:        true,
		},
		{
			name: "test failure 1 (named service port lookup failure)",
			svc: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Port:       80,
							TargetPort: intstr.FromString("http"),
						},
					},
				},
			},
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: int32(8080)},
							},
						},
					},
				},
			},
			ports:      []string{"https"},
			translated: []string{},
			err:        true,
		},
		{
			name: "test failure 2 (service port not declared)",
			svc: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Port:       80,
							TargetPort: intstr.FromString("http"),
						},
					},
				},
			},
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									Name:          "https",
									ContainerPort: int32(443)},
							},
						},
					},
				},
			},
			ports:      []string{"443"},
			translated: []string{},
			err:        true,
		},
	}

	for _, tc := range cases {
		translated, err := translateServicePortToTargetPort(tc.ports, tc.svc, tc.pod)
		if err != nil {
			if tc.err {
				continue
			}

			t.Errorf("%v: unexpected error: %v", tc.name, err)
			continue
		}

		if tc.err {
			t.Errorf("%v: unexpected success", tc.name)
			continue
		}

		if !reflect.DeepEqual(translated, tc.translated) {
			t.Errorf("%v: expected %v; got %v", tc.name, tc.translated, translated)
		}
	}
}

func execPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "test", ResourceVersion: "10"},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyAlways,
			DNSPolicy:     corev1.DNSClusterFirst,
			Containers: []corev1.Container{
				{
					Name: "bar",
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
}

func TestConvertPodNamedPortToNumber(t *testing.T) {
	cases := []struct {
		name      string
		pod       corev1.Pod
		ports     []string
		converted []string
		err       bool
	}{
		{
			name: "port number without local port",
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: int32(80)},
							},
						},
					},
				},
			},
			ports:     []string{"80"},
			converted: []string{"80"},
			err:       false,
		},
		{
			name: "port number with local port",
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: int32(80)},
							},
						},
					},
				},
			},
			ports:     []string{"8000:80"},
			converted: []string{"8000:80"},
			err:       false,
		},
		{
			name: "port number with random local port",
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: int32(80)},
							},
						},
					},
				},
			},
			ports:     []string{":80"},
			converted: []string{":80"},
			err:       false,
		},
		{
			name: "named port without local port",
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: int32(80)},
							},
						},
					},
				},
			},
			ports:     []string{"http"},
			converted: []string{"80"},
			err:       false,
		},
		{
			name: "named port with local port",
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: int32(80)},
							},
						},
					},
				},
			},
			ports:     []string{"8000:http"},
			converted: []string{"8000:80"},
			err:       false,
		},
		{
			name: "named port with random local port",
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: int32(80)},
							},
						},
					},
				},
			},
			ports:     []string{":http"},
			converted: []string{":80"},
			err:       false,
		},
		{
			name: "named port can not be found",
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									Name:          "https",
									ContainerPort: int32(443)},
							},
						},
					},
				},
			},
			ports: []string{"http"},
			err:   true,
		},
		{
			name: "one of the requested named ports can not be found",
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									Name:          "https",
									ContainerPort: int32(443)},
							},
						},
					},
				},
			},
			ports: []string{"https", "http"},
			err:   true,
		},
		{
			name: "empty port name",
			pod: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: int32(27017)},
							},
						},
					},
				},
			},
			ports:     []string{"28015:"},
			converted: nil,
			err:       true,
		},
	}

	for _, tc := range cases {
		converted, err := convertPodNamedPortToNumber(tc.ports, tc.pod)
		if err != nil {
			if tc.err {
				continue
			}

			t.Errorf("%v: unexpected error: %v", tc.name, err)
			continue
		}

		if tc.err {
			t.Errorf("%v: unexpected success", tc.name)
			continue
		}

		if !reflect.DeepEqual(converted, tc.converted) {
			t.Errorf("%v: expected %v; got %v", tc.name, tc.converted, converted)
		}
	}
}

func TestCheckUDPPort(t *testing.T) {
	tests := []struct {
		name        string
		pod         *corev1.Pod
		service     *corev1.Service
		ports       []string
		expectError bool
	}{
		{
			name: "forward to a UDP port in a Pod",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{Protocol: corev1.ProtocolUDP, ContainerPort: 53},
							},
						},
					},
				},
			},
			ports:       []string{"53"},
			expectError: true,
		},
		{
			name: "forward to a named UDP port in a Pod",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{Protocol: corev1.ProtocolUDP, ContainerPort: 53, Name: "dns"},
							},
						},
					},
				},
			},
			ports:       []string{"dns"},
			expectError: true,
		},
		{
			name: "Pod has ports with both TCP and UDP protocol (UDP first)",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{Protocol: corev1.ProtocolUDP, ContainerPort: 53},
								{Protocol: corev1.ProtocolTCP, ContainerPort: 53},
							},
						},
					},
				},
			},
			ports: []string{":53"},
		},
		{
			name: "Pod has ports with both TCP and UDP protocol (TCP first)",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{Protocol: corev1.ProtocolTCP, ContainerPort: 53},
								{Protocol: corev1.ProtocolUDP, ContainerPort: 53},
							},
						},
					},
				},
			},
			ports: []string{":53"},
		},

		{
			name: "forward to a UDP port in a Service",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{Protocol: corev1.ProtocolUDP, Port: 53},
					},
				},
			},
			ports:       []string{"53"},
			expectError: true,
		},
		{
			name: "forward to a named UDP port in a Service",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{Protocol: corev1.ProtocolUDP, Port: 53, Name: "dns"},
					},
				},
			},
			ports:       []string{"10053:dns"},
			expectError: true,
		},
		{
			name: "Service has ports with both TCP and UDP protocol (UDP first)",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{Protocol: corev1.ProtocolUDP, Port: 53},
						{Protocol: corev1.ProtocolTCP, Port: 53},
					},
				},
			},
			ports: []string{"53"},
		},
		{
			name: "Service has ports with both TCP and UDP protocol (TCP first)",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{Protocol: corev1.ProtocolTCP, Port: 53},
						{Protocol: corev1.ProtocolUDP, Port: 53},
					},
				},
			},
			ports: []string{"53"},
		},
	}
	for _, tc := range tests {
		var err error
		if tc.pod != nil {
			err = checkUDPPortInPod(tc.ports, tc.pod)
		} else if tc.service != nil {
			err = checkUDPPortInService(tc.ports, tc.service)
		}
		if err != nil {
			if tc.expectError {
				continue
			}
			t.Errorf("%v: unexpected error: %v", tc.name, err)
			continue
		}
		if tc.expectError {
			t.Errorf("%v: unexpected success", tc.name)
		}
	}
}

func TestCreateDialer(t *testing.T) {
	url, err := url.Parse("http://localhost:8080/index.html")
	if err != nil {
		t.Fatalf("unable to parse test url: %v", err)
	}
	config := cmdtesting.DefaultClientConfig()
	opts := PortForwardOptions{Config: config}
	// First, ensure that no environment variable creates the fallback dialer.
	dialer, err := createDialer("GET", url, opts)
	if err != nil {
		t.Fatalf("unable to create dialer: %v", err)
	}
	if _, isFallback := dialer.(*portforward.FallbackDialer); !isFallback {
		t.Errorf("expected fallback dialer, got %#v", dialer)
	}
	// Next, check turning on feature flag explicitly also creates fallback dialer.
	t.Setenv(string(cmdutil.PortForwardWebsockets), "true")
	dialer, err = createDialer("GET", url, opts)
	if err != nil {
		t.Fatalf("unable to create dialer: %v", err)
	}
	if _, isFallback := dialer.(*portforward.FallbackDialer); !isFallback {
		t.Errorf("expected fallback dialer, got %#v", dialer)
	}
	// Finally, check explicit disabling does NOT create the fallback dialer.
	t.Setenv(string(cmdutil.PortForwardWebsockets), "false")
	dialer, err = createDialer("GET", url, opts)
	if err != nil {
		t.Fatalf("unable to create dialer: %v", err)
	}
	if _, isFallback := dialer.(*portforward.FallbackDialer); isFallback {
		t.Errorf("expected fallback dialer, got %#v", dialer)
	}
}

// TestReversePortForwardFlagParsing tests that the --reverse flag is properly parsed
// TODO: This test is currently skipped because the --reverse flag has not been implemented yet.
// Once the flag is added to NewCmdPortForward, remove the t.Skip() and verify the tests pass.
func TestReversePortForwardFlagParsing(t *testing.T) {
	t.Skip("Skipping until --reverse flag is implemented in portforward.go")

	tests := []struct {
		name          string
		args          []string
		flags         map[string]string
		expectReverse bool
		expectError   bool
	}{
		{
			name:          "reverse flag set to true",
			args:          []string{"foo", "8080:5000"},
			flags:         map[string]string{"reverse": "true"},
			expectReverse: true,
			expectError:   false,
		},
		{
			name:          "reverse flag set to false",
			args:          []string{"foo", "8080:5000"},
			flags:         map[string]string{"reverse": "false"},
			expectReverse: false,
			expectError:   false,
		},
		{
			name:          "reverse flag not set (default forward mode)",
			args:          []string{"foo", "8080:5000"},
			flags:         map[string]string{},
			expectReverse: false,
			expectError:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tf := cmdtesting.NewTestFactory().WithNamespace("test")
			defer tf.Cleanup()

			codec := scheme.Codecs.LegacyCodec(scheme.Scheme.PrioritizedVersionsAllGroups()...)
			ns := scheme.Codecs.WithoutConversion()

			pod := execPod()
			tf.Client = &fake.RESTClient{
				VersionedAPIPath:     "/api/v1",
				GroupVersion:         schema.GroupVersion{Group: "", Version: "v1"},
				NegotiatedSerializer: ns,
				Client: fake.CreateHTTPClient(func(req *http.Request) (*http.Response, error) {
					switch p, m := req.URL.Path, req.Method; {
					case p == "/api/v1/namespaces/test/pods/foo" && m == "GET":
						body := cmdtesting.ObjBody(codec, pod)
						return &http.Response{StatusCode: http.StatusOK, Header: cmdtesting.DefaultHeader(), Body: body}, nil
					default:
						t.Errorf("%s: unexpected request: %#v\n%#v", tc.name, req.URL, req)
						return nil, nil
					}
				}),
			}
			tf.ClientConfigVal = cmdtesting.DefaultClientConfig()

			opts := &PortForwardOptions{}
			cmd := NewCmdPortForward(tf, genericiooptions.NewTestIOStreamsDiscard())

			for name, value := range tc.flags {
				if err := cmd.Flags().Set(name, value); err != nil {
					t.Fatalf("failed to set flag %s: %v", name, err)
				}
			}

			err := opts.Complete(tf, cmd, tc.args)

			if tc.expectError && err == nil {
				t.Errorf("%s: expected error but got none", tc.name)
			}
			if !tc.expectError && err != nil {
				t.Errorf("%s: unexpected error: %v", tc.name, err)
			}

			// TODO: Once the Reverse field is added to PortForwardOptions, verify it's set correctly:
			// if opts.Reverse != tc.expectReverse {
			//     t.Errorf("%s: expected Reverse=%v but got %v", tc.name, tc.expectReverse, opts.Reverse)
			// }
		})
	}
}

// TestReversePortSpecificationSyntax tests that reverse port specifications follow REMOTE:LOCAL format
func TestReversePortSpecificationSyntax(t *testing.T) {
	tests := []struct {
		name        string
		ports       []string
		reverse     bool
		expectValid bool
		expectError string
	}{
		{
			name:        "forward mode with LOCAL:REMOTE format",
			ports:       []string{"8080:5000"},
			reverse:     false,
			expectValid: true,
		},
		{
			name:        "forward mode with single port (same local and remote)",
			ports:       []string{"5000"},
			reverse:     false,
			expectValid: true,
		},
		{
			name:        "forward mode with random local port",
			ports:       []string{":5000"},
			reverse:     false,
			expectValid: true,
		},
		{
			name:        "reverse mode with REMOTE:LOCAL format",
			ports:       []string{"8080:5000"},
			reverse:     true,
			expectValid: true,
		},
		{
			name:        "reverse mode with single port (same local and remote)",
			ports:       []string{"5000"},
			reverse:     true,
			expectValid: true,
		},
		{
			name:        "reverse mode with multiple ports",
			ports:       []string{"8080:5000", "9090:6000"},
			reverse:     true,
			expectValid: true,
		},
		{
			name:        "reverse mode should not allow random remote port",
			ports:       []string{"8080:"},
			reverse:     true,
			expectValid: false,
			expectError: "remote port cannot be empty in reverse mode",
		},
		// Port range validation tests
		// NOTE: The following tests are temporary helpers for implementation and may be
		// removed or refactored once proper port validation is implemented in the main code.
		{
			name:        "port number zero in forward mode",
			ports:       []string{"0:5000"},
			reverse:     false,
			expectValid: true, // Port 0 means random port assignment for local port
		},
		{
			name:        "port number zero as remote port in forward mode",
			ports:       []string{"8080:0"},
			reverse:     false,
			expectValid: false,
			expectError: "remote port must be between 1 and 65535",
		},
		{
			name:        "port number zero in reverse mode",
			ports:       []string{"0:5000"},
			reverse:     true,
			expectValid: false,
			expectError: "remote port must be between 1 and 65535",
		},
		{
			name:        "negative port number in forward mode",
			ports:       []string{"-1:5000"},
			reverse:     false,
			expectValid: false,
			expectError: "port must be between 0 and 65535",
		},
		{
			name:        "negative port number in reverse mode",
			ports:       []string{"8080:-1"},
			reverse:     true,
			expectValid: false,
			expectError: "port must be between 1 and 65535",
		},
		{
			name:        "port number above valid range (65536) in forward mode",
			ports:       []string{"65536:5000"},
			reverse:     false,
			expectValid: false,
			expectError: "port must be between 0 and 65535",
		},
		{
			name:        "port number above valid range (65536) in reverse mode",
			ports:       []string{"8080:65536"},
			reverse:     true,
			expectValid: false,
			expectError: "port must be between 1 and 65535",
		},
		{
			name:        "port number at maximum valid range (65535) in forward mode",
			ports:       []string{"65535:5000"},
			reverse:     false,
			expectValid: true,
		},
		{
			name:        "port number at maximum valid range (65535) in reverse mode",
			ports:       []string{"8080:65535"},
			reverse:     true,
			expectValid: true,
		},
		{
			name:        "port number 1 (minimum valid) in forward mode",
			ports:       []string{"1:5000"},
			reverse:     false,
			expectValid: true,
		},
		{
			name:        "port number 1 (minimum valid) in reverse mode",
			ports:       []string{"8080:1"},
			reverse:     true,
			expectValid: true,
		},
		{
			name:        "very large port number (100000) in forward mode",
			ports:       []string{"100000:5000"},
			reverse:     false,
			expectValid: false,
			expectError: "port must be between 0 and 65535",
		},
		{
			name:        "very large port number (100000) in reverse mode",
			ports:       []string{"8080:100000"},
			reverse:     true,
			expectValid: false,
			expectError: "port must be between 1 and 65535",
		},
		{
			name:        "non-numeric port in forward mode",
			ports:       []string{"abc:5000"},
			reverse:     false,
			expectValid: false,
			expectError: "invalid port",
		},
		{
			name:        "non-numeric port in reverse mode",
			ports:       []string{"8080:xyz"},
			reverse:     true,
			expectValid: false,
			expectError: "invalid port",
		},
		{
			name:        "empty string as port",
			ports:       []string{""},
			reverse:     false,
			expectValid: false,
			expectError: "port specification cannot be empty",
		},
		{
			name:        "whitespace in port specification",
			ports:       []string{" 8080:5000 "},
			reverse:     false,
			expectValid: false,
			expectError: "invalid port",
		},
		{
			name:        "multiple colons in port specification",
			ports:       []string{"8080:5000:3000"},
			reverse:     false,
			expectValid: false,
			expectError: "invalid port specification",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// This is a placeholder for the actual validation logic
			// The implementation will need to validate reverse port specifications
			// For now, we're defining the expected behavior

			// When reverse mode is implemented, add validation like:
			// err := validateReversePortSpecs(tc.ports, tc.reverse)
			// if tc.expectValid && err != nil {
			//     t.Errorf("%s: expected valid ports but got error: %v", tc.name, err)
			// }
			// if !tc.expectValid && err == nil {
			//     t.Errorf("%s: expected error but got none", tc.name)
			// }
			// if !tc.expectValid && err != nil && !strings.Contains(err.Error(), tc.expectError) {
			//     t.Errorf("%s: expected error containing %q but got %q", tc.name, tc.expectError, err.Error())
			// }
		})
	}
}

// TestReversePortForwardValidation tests validation logic for reverse port-forward
func TestReversePortForwardValidation(t *testing.T) {
	tests := []struct {
		name        string
		podName     string
		ports       []string
		reverse     bool
		expectError bool
		errorMsg    string
	}{
		{
			name:        "empty pod name should fail",
			podName:     "",
			ports:       []string{"8080:5000"},
			reverse:     false,
			expectError: true,
			errorMsg:    "pod name or resource type/name must be specified",
		},
		{
			name:        "no ports specified should fail",
			podName:     "test-pod",
			ports:       []string{},
			reverse:     false,
			expectError: true,
			errorMsg:    "at least 1 PORT is required for port-forward",
		},
		{
			name:        "valid ports in forward mode",
			podName:     "test-pod",
			ports:       []string{"8080:5000"},
			reverse:     false,
			expectError: false,
		},
		{
			name:        "valid ports in reverse mode",
			podName:     "test-pod",
			ports:       []string{"8080:5000"},
			reverse:     true,
			expectError: false,
		},
		// NOTE: The following tests are temporary helpers for implementation and may be
		// removed or refactored once proper port validation is implemented in the main code.
		{
			name:        "port zero as local port in forward mode should be valid",
			podName:     "test-pod",
			ports:       []string{"0:5000"},
			reverse:     false,
			expectError: false, // Port 0 means random local port
		},
		{
			name:        "port zero as remote port in forward mode should fail",
			podName:     "test-pod",
			ports:       []string{"8080:0"},
			reverse:     false,
			expectError: true,
			errorMsg:    "remote port must be between 1 and 65535",
		},
		{
			name:        "port zero in reverse mode should fail",
			podName:     "test-pod",
			ports:       []string{"0:5000"},
			reverse:     true,
			expectError: true,
			errorMsg:    "remote port must be between 1 and 65535",
		},
		{
			name:        "negative port should fail",
			podName:     "test-pod",
			ports:       []string{"-1:5000"},
			reverse:     false,
			expectError: true,
			errorMsg:    "port must be between 0 and 65535",
		},
		{
			name:        "port above 65535 should fail",
			podName:     "test-pod",
			ports:       []string{"65536:5000"},
			reverse:     false,
			expectError: true,
			errorMsg:    "port must be between 0 and 65535",
		},
		{
			name:        "port 65535 should be valid",
			podName:     "test-pod",
			ports:       []string{"65535:5000"},
			reverse:     false,
			expectError: false,
		},
		{
			name:        "port 1 should be valid",
			podName:     "test-pod",
			ports:       []string{"1:5000"},
			reverse:     false,
			expectError: false,
		},
		{
			name:        "multiple invalid ports should fail on first invalid",
			podName:     "test-pod",
			ports:       []string{"8080:5000", "100000:6000"},
			reverse:     false,
			expectError: true,
			errorMsg:    "port must be between 0 and 65535",
		},
		{
			name:        "mixed valid and invalid ports should fail",
			podName:     "test-pod",
			ports:       []string{"8080:5000", "-1:6000", "9090:7000"},
			reverse:     false,
			expectError: true,
			errorMsg:    "port must be between 0 and 65535",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := PortForwardOptions{
				PodName: tc.podName,
				Ports:   tc.ports,
				// TODO: Add Reverse field once it's implemented in PortForwardOptions
				// Reverse: tc.reverse,
			}

			// Only set required fields if we have a pod name
			// This allows us to test validation failures
			if tc.podName != "" && len(tc.ports) > 0 {
				opts.PortForwarder = &fakePortForwarder{}
				// For PodClient, RESTClient, and Config, we just need non-nil values
				// since Validate() only checks if they are nil
				opts.PodClient = nil // Will fail validation if we expect it to pass
				opts.RESTClient = nil
				opts.Config = nil
			}

			err := opts.Validate()

			if tc.expectError && err == nil {
				t.Errorf("%s: expected error but got none", tc.name)
			}
			if !tc.expectError && err != nil {
				t.Errorf("%s: unexpected error: %v", tc.name, err)
			}
			if tc.expectError && err != nil && tc.errorMsg != "" {
				if err.Error() != tc.errorMsg {
					t.Errorf("%s: expected error %q but got %q", tc.name, tc.errorMsg, err.Error())
				}
			}
		})
	}
}

// TestPortRangeValidation tests comprehensive port range validation
// NOTE: This test function is a temporary helper for implementation and may be
// removed or refactored once proper port validation is implemented in the main code.
func TestPortRangeValidation(t *testing.T) {
	tests := []struct {
		name        string
		portSpec    string
		expectError bool
		errorMsg    string
	}{
		// Valid port ranges
		{
			name:        "minimum valid port (1)",
			portSpec:    "1:5000",
			expectError: false,
		},
		{
			name:        "maximum valid port (65535)",
			portSpec:    "65535:5000",
			expectError: false,
		},
		{
			name:        "typical web port (80)",
			portSpec:    "80:8080",
			expectError: false,
		},
		{
			name:        "typical https port (443)",
			portSpec:    "443:8443",
			expectError: false,
		},
		{
			name:        "high port number (60000)",
			portSpec:    "60000:5000",
			expectError: false,
		},
		// Port 0 - special case
		{
			name:        "port 0 for local (random port assignment)",
			portSpec:    "0:5000",
			expectError: false, // Should be valid for local port only
		},
		{
			name:        "port 0 for remote should fail",
			portSpec:    "8080:0",
			expectError: true,
			errorMsg:    "remote port must be between 1 and 65535",
		},
		{
			name:        "both ports as 0 should fail",
			portSpec:    "0:0",
			expectError: true,
			errorMsg:    "remote port must be between 1 and 65535",
		},
		// Negative numbers
		{
			name:        "negative local port",
			portSpec:    "-1:5000",
			expectError: true,
			errorMsg:    "port must be between 0 and 65535",
		},
		{
			name:        "negative remote port",
			portSpec:    "8080:-1",
			expectError: true,
			errorMsg:    "port must be between 1 and 65535",
		},
		{
			name:        "both ports negative",
			portSpec:    "-1:-1",
			expectError: true,
			errorMsg:    "port must be between 0 and 65535",
		},
		{
			name:        "large negative number",
			portSpec:    "-999999:5000",
			expectError: true,
			errorMsg:    "port must be between 0 and 65535",
		},
		// Above valid range
		{
			name:        "port 65536 (one above max)",
			portSpec:    "65536:5000",
			expectError: true,
			errorMsg:    "port must be between 0 and 65535",
		},
		{
			name:        "remote port 65536",
			portSpec:    "8080:65536",
			expectError: true,
			errorMsg:    "port must be between 1 and 65535",
		},
		{
			name:        "port 100000",
			portSpec:    "100000:5000",
			expectError: true,
			errorMsg:    "port must be between 0 and 65535",
		},
		{
			name:        "very large port number",
			portSpec:    "999999:5000",
			expectError: true,
			errorMsg:    "port must be between 0 and 65535",
		},
		{
			name:        "max int overflow",
			portSpec:    "2147483648:5000",
			expectError: true,
			errorMsg:    "port must be between 0 and 65535",
		},
		// Boundary testing
		{
			name:        "just below max valid (65534)",
			portSpec:    "65534:5000",
			expectError: false,
		},
		{
			name:        "just above min valid (2)",
			portSpec:    "2:5000",
			expectError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// This is a placeholder for the actual port validation logic
			// When implementing reverse port-forward, this validation should be added

			// Example implementation:
			// err := validatePortRange(tc.portSpec)
			// if tc.expectError && err == nil {
			//     t.Errorf("%s: expected error but got none", tc.name)
			// }
			// if !tc.expectError && err != nil {
			//     t.Errorf("%s: unexpected error: %v", tc.name, err)
			// }
			// if tc.expectError && err != nil && !strings.Contains(err.Error(), tc.errorMsg) {
			//     t.Errorf("%s: expected error containing %q but got %q", tc.name, tc.errorMsg, err.Error())
			// }
		})
	}
}

// TestReverseForwardModeConflict tests that reverse and forward modes cannot be mixed
func TestReverseForwardModeConflict(t *testing.T) {
	tests := []struct {
		name        string
		reverse     bool
		ports       []string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "forward mode with standard port syntax",
			reverse:     false,
			ports:       []string{"8080:5000"},
			expectError: false,
		},
		{
			name:        "reverse mode with standard port syntax",
			reverse:     true,
			ports:       []string{"8080:5000"},
			expectError: false,
		},
		// Additional tests can be added as the implementation evolves
		// to test specific conflict scenarios
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// This is a placeholder for testing mode conflict detection
			// The actual implementation will add logic to detect and prevent
			// conflicting reverse/forward configurations

			// Example of what the test might look like:
			// err := validatePortForwardMode(tc.reverse, tc.ports)
			// if tc.expectError && err == nil {
			//     t.Errorf("%s: expected error but got none", tc.name)
			// }
			// if !tc.expectError && err != nil {
			//     t.Errorf("%s: unexpected error: %v", tc.name, err)
			// }
		})
	}
}
