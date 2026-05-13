package main

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/yourorg/etcd-ui/internal/etcdpool"
	"github.com/yourorg/etcd-ui/internal/httpx"
	"github.com/yourorg/etcd-ui/internal/models"

	authpb "go.etcd.io/etcd/api/v3/authpb"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func rbacHandlers(pool *etcdpool.Pool) func(chi.Router) {
	return func(r chi.Router) {
		r.Get("/clusters/{id}/rbac/status", rbacStatus(pool))
		r.Post("/clusters/{id}/rbac/enable", rbacEnable(pool))
		r.Post("/clusters/{id}/rbac/disable", rbacDisable(pool))

		r.Get("/clusters/{id}/rbac/users", listUsers(pool))
		r.Post("/clusters/{id}/rbac/users", addUser(pool))
		r.Delete("/clusters/{id}/rbac/users/{name}", deleteUser(pool))
		r.Post("/clusters/{id}/rbac/users/{name}/roles", grantRole(pool))
		r.Delete("/clusters/{id}/rbac/users/{name}/roles/{role}", revokeRole(pool))

		r.Get("/clusters/{id}/rbac/roles", listRoles(pool))
		r.Post("/clusters/{id}/rbac/roles", addRole(pool))
		r.Delete("/clusters/{id}/rbac/roles/{name}", deleteRole(pool))
		r.Post("/clusters/{id}/rbac/roles/{name}/permissions", grantPermission(pool))
	}
}

func rbacStatus(pool *etcdpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cli, _, err := pool.Client(chi.URLParam(r, "id"))
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		resp, err := cli.AuthStatus(ctx)
		if err != nil {
			httpx.Err(w, 502, err)
			return
		}
		httpx.JSON(w, 200, map[string]any{"enabled": resp.Enabled, "revision": resp.AuthRevision})
	}
}

func rbacEnable(pool *etcdpool.Pool) http.HandlerFunc {
	return doSimple(pool, func(ctx context.Context, cli *clientv3.Client) error {
		_, err := cli.AuthEnable(ctx)
		return err
	})
}

func rbacDisable(pool *etcdpool.Pool) http.HandlerFunc {
	return doSimple(pool, func(ctx context.Context, cli *clientv3.Client) error {
		_, err := cli.AuthDisable(ctx)
		return err
	})
}

func listUsers(pool *etcdpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cli, _, err := pool.Client(chi.URLParam(r, "id"))
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		users, err := cli.UserList(ctx)
		if err != nil {
			httpx.Err(w, 502, err)
			return
		}
		out := make([]models.RBACUser, 0, len(users.Users))
		for _, u := range users.Users {
			ug, err := cli.UserGet(ctx, u)
			if err != nil {
				out = append(out, models.RBACUser{Name: u})
				continue
			}
			out = append(out, models.RBACUser{Name: u, Roles: ug.Roles})
		}
		httpx.JSON(w, 200, out)
	}
}

func addUser(pool *etcdpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cli, _, err := pool.Client(chi.URLParam(r, "id"))
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		var req models.RBACUserCreate
		if err := httpx.Decode(r, &req); err != nil {
			httpx.Err(w, 400, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if _, err := cli.UserAdd(ctx, req.Name, req.Password); err != nil {
			httpx.Err(w, 502, err)
			return
		}
		httpx.JSON(w, 201, models.RBACUser{Name: req.Name})
	}
}

func deleteUser(pool *etcdpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cli, _, err := pool.Client(chi.URLParam(r, "id"))
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if _, err := cli.UserDelete(ctx, chi.URLParam(r, "name")); err != nil {
			httpx.Err(w, 502, err)
			return
		}
		w.WriteHeader(204)
	}
}

func grantRole(pool *etcdpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cli, _, err := pool.Client(chi.URLParam(r, "id"))
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		var req models.RBACRoleGrant
		if err := httpx.Decode(r, &req); err != nil {
			httpx.Err(w, 400, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if _, err := cli.UserGrantRole(ctx, chi.URLParam(r, "name"), req.Role); err != nil {
			httpx.Err(w, 502, err)
			return
		}
		w.WriteHeader(204)
	}
}

func revokeRole(pool *etcdpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cli, _, err := pool.Client(chi.URLParam(r, "id"))
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if _, err := cli.UserRevokeRole(ctx, chi.URLParam(r, "name"), chi.URLParam(r, "role")); err != nil {
			httpx.Err(w, 502, err)
			return
		}
		w.WriteHeader(204)
	}
}

func listRoles(pool *etcdpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cli, _, err := pool.Client(chi.URLParam(r, "id"))
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		roles, err := cli.RoleList(ctx)
		if err != nil {
			httpx.Err(w, 502, err)
			return
		}
		out := make([]models.RBACRole, 0, len(roles.Roles))
		for _, rn := range roles.Roles {
			rg, err := cli.RoleGet(ctx, rn)
			if err != nil {
				out = append(out, models.RBACRole{Name: rn})
				continue
			}
			role := models.RBACRole{Name: rn}
			for _, p := range rg.Perm {
				role.Permissions = append(role.Permissions, models.RBACPermission{
					Type:     permTypeName(p.PermType),
					Key:      string(p.Key),
					RangeEnd: string(p.RangeEnd),
				})
			}
			out = append(out, role)
		}
		httpx.JSON(w, 200, out)
	}
}

func addRole(pool *etcdpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cli, _, err := pool.Client(chi.URLParam(r, "id"))
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		var req models.RBACRole
		if err := httpx.Decode(r, &req); err != nil {
			httpx.Err(w, 400, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if _, err := cli.RoleAdd(ctx, req.Name); err != nil {
			httpx.Err(w, 502, err)
			return
		}
		httpx.JSON(w, 201, req)
	}
}

func deleteRole(pool *etcdpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cli, _, err := pool.Client(chi.URLParam(r, "id"))
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if _, err := cli.RoleDelete(ctx, chi.URLParam(r, "name")); err != nil {
			httpx.Err(w, 502, err)
			return
		}
		w.WriteHeader(204)
	}
}

func grantPermission(pool *etcdpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cli, _, err := pool.Client(chi.URLParam(r, "id"))
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		var req models.RBACPermission
		if err := httpx.Decode(r, &req); err != nil {
			httpx.Err(w, 400, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		var pt clientv3.PermissionType
		switch req.Type {
		case "read":
			pt = clientv3.PermissionType(authpb.READ)
		case "write":
			pt = clientv3.PermissionType(authpb.WRITE)
		default:
			pt = clientv3.PermissionType(authpb.READWRITE)
		}
		end := req.RangeEnd
		if req.Prefix {
			end = clientv3.GetPrefixRangeEnd(req.Key)
		}
		if _, err := cli.RoleGrantPermission(ctx, chi.URLParam(r, "name"), req.Key, end, pt); err != nil {
			httpx.Err(w, 502, err)
			return
		}
		w.WriteHeader(204)
	}
}

func permTypeName(p authpb.Permission_Type) string {
	switch p {
	case authpb.READ:
		return "read"
	case authpb.WRITE:
		return "write"
	case authpb.READWRITE:
		return "readwrite"
	}
	return "unknown"
}

// doSimple wraps a tiny "find client, call function, 204" handler.
func doSimple(pool *etcdpool.Pool, fn func(context.Context, *clientv3.Client) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cli, _, err := pool.Client(chi.URLParam(r, "id"))
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := fn(ctx, cli); err != nil {
			httpx.Err(w, 502, err)
			return
		}
		httpx.JSON(w, 200, map[string]bool{"ok": true})
	}
}
