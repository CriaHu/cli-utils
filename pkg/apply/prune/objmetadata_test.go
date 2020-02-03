// Copyright 2020 The Kubernetes Authors.
// SPDX-License-Identifier: Apache-2.0

package prune

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestCreateObjMetadata(t *testing.T) {
	tests := []struct {
		namespace string
		name      string
		gk        schema.GroupKind
		expected  string
		isError   bool
	}{
		{
			namespace: "  \n",
			name:      " test-name\t",
			gk: schema.GroupKind{
				Group: "apps",
				Kind:  "ReplicaSet",
			},
			expected: "_test-name_apps_ReplicaSet",
			isError:  false,
		},
		{
			namespace: "test-namespace ",
			name:      " test-name\t",
			gk: schema.GroupKind{
				Group: "apps",
				Kind:  "ReplicaSet",
			},
			expected: "test-namespace_test-name_apps_ReplicaSet",
			isError:  false,
		},
		// Error with empty name.
		{
			namespace: "test-namespace ",
			name:      " \t",
			gk: schema.GroupKind{
				Group: "apps",
				Kind:  "ReplicaSet",
			},
			expected: "",
			isError:  true,
		},
		// Error with empty GroupKind.
		{
			namespace: "test-namespace",
			name:      "test-name",
			gk:        schema.GroupKind{},
			expected:  "",
			isError:   true,
		},
	}

	for _, test := range tests {
		inv, err := createObjMetadata(test.namespace, test.name, test.gk)
		if !test.isError {
			if err != nil {
				t.Errorf("Error creating inventory when it should have worked.")
			} else if test.expected != inv.String() {
				t.Errorf("Expected inventory (%s) != created inventory(%s)\n", test.expected, inv.String())
			}
		}
		if test.isError && err == nil {
			t.Errorf("Should have returned an error in createObjMetadata()")
		}
	}
}

func TestObjMetadataEqual(t *testing.T) {
	tests := []struct {
		inventory1 *ObjMetadata
		inventory2 *ObjMetadata
		isEqual    bool
	}{
		// "Other" inventory is nil, then not equal.
		{
			inventory1: &ObjMetadata{
				Name: "test-inv",
				GroupKind: schema.GroupKind{
					Group: "apps",
					Kind:  "Deployment",
				},
			},
			inventory2: nil,
			isEqual:    false,
		},
		// Two equal inventories without a namespace
		{
			inventory1: &ObjMetadata{
				Name: "test-inv",
				GroupKind: schema.GroupKind{
					Group: "apps",
					Kind:  "Deployment",
				},
			},
			inventory2: &ObjMetadata{
				Name: "test-inv",
				GroupKind: schema.GroupKind{
					Group: "apps",
					Kind:  "Deployment",
				},
			},
			isEqual: true,
		},
		// Two equal inventories with a namespace
		{
			inventory1: &ObjMetadata{
				Namespace: "test-namespace",
				Name:      "test-inv",
				GroupKind: schema.GroupKind{
					Group: "apps",
					Kind:  "Deployment",
				},
			},
			inventory2: &ObjMetadata{
				Namespace: "test-namespace",
				Name:      "test-inv",
				GroupKind: schema.GroupKind{
					Group: "apps",
					Kind:  "Deployment",
				},
			},
			isEqual: true,
		},
		// One inventory with a namespace, one without -- not equal.
		{
			inventory1: &ObjMetadata{
				Name: "test-inv",
				GroupKind: schema.GroupKind{
					Group: "apps",
					Kind:  "Deployment",
				},
			},
			inventory2: &ObjMetadata{
				Namespace: "test-namespace",
				Name:      "test-inv",
				GroupKind: schema.GroupKind{
					Group: "apps",
					Kind:  "Deployment",
				},
			},
			isEqual: false,
		},
		// One inventory with a Deployment, one with a ReplicaSet -- not equal.
		{
			inventory1: &ObjMetadata{
				Name: "test-inv",
				GroupKind: schema.GroupKind{
					Group: "apps",
					Kind:  "Deployment",
				},
			},
			inventory2: &ObjMetadata{
				Name: "test-inv",
				GroupKind: schema.GroupKind{
					Group: "apps",
					Kind:  "ReplicaSet",
				},
			},
			isEqual: false,
		},
	}

	for _, test := range tests {
		actual := test.inventory1.Equals(test.inventory2)
		if test.isEqual && !actual {
			t.Errorf("Expected inventories equal, but actual is not: (%s)/(%s)\n", test.inventory1, test.inventory2)
		}
	}
}

func TestParseObjMetadata(t *testing.T) {
	tests := []struct {
		invStr    string
		inventory *ObjMetadata
		isError   bool
	}{
		{
			invStr: "_test-name_apps_ReplicaSet\t",
			inventory: &ObjMetadata{
				Name: "test-name",
				GroupKind: schema.GroupKind{
					Group: "apps",
					Kind:  "ReplicaSet",
				},
			},
			isError: false,
		},
		{
			invStr: "test-namespace_test-name_apps_Deployment",
			inventory: &ObjMetadata{
				Namespace: "test-namespace",
				Name:      "test-name",
				GroupKind: schema.GroupKind{
					Group: "apps",
					Kind:  "Deployment",
				},
			},
			isError: false,
		},
		// Not enough fields -- error
		{
			invStr:    "_test-name_apps",
			inventory: &ObjMetadata{},
			isError:   true,
		},
	}

	for _, test := range tests {
		actual, err := parseObjMetadata(test.invStr)
		if !test.isError {
			if err != nil {
				t.Errorf("Error parsing inventory when it should have worked.")
			} else if !test.inventory.Equals(actual) {
				t.Errorf("Expected inventory (%s) != parsed inventory (%s)\n", test.inventory, actual)
			}
		}
		if test.isError && err == nil {
			t.Errorf("Should have returned an error in parseObjMetadata()")
		}
	}
}
