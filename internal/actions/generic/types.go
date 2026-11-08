package generic

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type metav1DeletionPropagation = metav1.DeletionPropagation

func ptType(contentType string) types.PatchType { return types.PatchType(contentType) }
