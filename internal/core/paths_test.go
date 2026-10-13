package core

import (
	"reflect"
	"testing"
)

func TestJoinPath(t *testing.T) {
	cases := []struct {
		parts []string
		want  string
	}{
		{[]string{"/api", "/"}, "/api"},
		{[]string{"/api/", "users/"}, "/api/users"},
		{[]string{"", "/"}, "/"},
		{[]string{"/", "*"}, "/*"},
		{[]string{"/a", ":id([^/]+)"}, "/a/:id([^/]+)"},
	}
	for _, c := range cases {
		if got := JoinPath(c.parts...); got != c.want {
			t.Errorf("JoinPath(%q) = %q, want %q", c.parts, got, c.want)
		}
	}
}

func TestSubstitute(t *testing.T) {
	// the brief: ids -> 1, other params -> test; plus uuid / enum / typed params
	if got := Substitute("/u/{id}/{userId}/{user_id}/{slug}", nil); got != "/u/1/1/1/test" {
		t.Errorf("got %q", got)
	}
	params := []PathParam{{Name: "token", Format: "uuid"}, {Name: "n", Type: "integer"}, {Name: "kind", Enum: []string{"a", "b"}}}
	if got := Substitute("/t/{token}/{n}/{kind}", params); got != "/t/"+SampleUUID+"/1/a" {
		t.Errorf("got %q", got)
	}
}

func TestNormalizeConcrete(t *testing.T) {
	cases := map[string]string{
		"/products/1":                      "/products/{id}",
		"/products/2":                      "/products/{id}",
		"/users/7/posts/9":                 "/users/{userId}/posts/{postId}",
		"/categories/3/items/4":            "/categories/{categoryId}/items/{itemId}",
		"/orders/5f8d0d55b54764421b7156c3": "/orders/{id}",
		"/api/v1/about":                    "/api/v1/about",
	}
	for in, want := range cases {
		if got, _ := NormalizeConcrete(in); got != want {
			t.Errorf("NormalizeConcrete(%q) = %q, want %q", in, got, want)
		}
	}
	_, params := NormalizeConcrete("/files/123e4567-e89b-12d3-a456-426614174000")
	if want := []PathParam{{Name: "id", Type: "string", Format: "uuid"}}; !reflect.DeepEqual(params, want) {
		t.Errorf("params = %+v", params)
	}
	if got := Shape("/users/{userId}/posts/{id}"); got != "/users/{}/posts/{}" {
		t.Errorf("Shape = %q", got)
	}
}
