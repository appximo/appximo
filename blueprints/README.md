# Appximo Blueprints

Blueprints are ready-to-use `schema.json` templates for common API patterns.
Each blueprint is a fully valid Appximo schema with resources, RBAC roles, and
pre-configured hooks.

## How to use

```bash
# List available blueprints
appximo blueprints list

# Create a new project from a blueprint
appximo init --blueprint fintech mi-fintech-api
cd mi-fintech-api

# Validate the schema (already valid out of the box)
appximo validate schema.json

# Generate and deploy
appximo generate schema.json
appximo serve --schema schema.json
```

---

## ecommerce

**File:** `blueprints/ecommerce.json`  
**Use case:** Tiendas online, marketplaces, catálogos de productos.

### Recursos

| Recurso | Campos clave | Relaciones |
|---|---|---|
| `products` | name, price, stock, sku, active | → categories |
| `categories` | name, slug, parent_id (self) | self-reference |
| `customers` | name, email, phone, address | — |
| `orders` | status, total, notes | → customers |

### Roles RBAC

| Rol | Recursos | Acciones |
|---|---|---|
| `admin` | `*` | `*` |
| `vendor` | products, categories, orders | read, create, update |
| `customer` | products, categories | read |
| `public` | products | read (solo fields: name, price, sku) |

### Hooks pre-configurados

- **products › before_create (JS):** Rechaza precio ≤ 0 antes de insertar.

---

## fintech

**File:** `blueprints/fintech.json`  
**Use case:** Billeteras digitales, banca, aplicaciones de pagos.

### Recursos

| Recurso | Campos clave | Relaciones |
|---|---|---|
| `accounts` | owner_id, type, balance, currency, status | — |
| `transactions` | account_id, amount, type, reference, status | → accounts |
| `beneficiaries` | owner_id, name, account_number, bank_code | — |

### Roles RBAC

| Rol | Recursos | Acciones | Condición |
|---|---|---|---|
| `super_admin` | `*` | `*` | — |
| `agent` | `*` | read, create, update | — |
| `customer` | accounts, transactions, beneficiaries | read, create | `owner_id = $user_id` |
| `auditor` | `*` | read | — |

### Hooks pre-configurados

- **transactions › before_create (JS):**
  - Rechaza amount ≤ 0.
  - Rechaza débitos superiores a 1,000,000 (límite anti-fraude configurable).

---

## crm

**File:** `blueprints/crm.json`  
**Use case:** Gestión de clientes, pipeline de ventas, seguimiento de actividades.

### Recursos

| Recurso | Campos clave | Relaciones |
|---|---|---|
| `contacts` | name, email, phone, company, status | → users |
| `deals` | title, stage, value, close_date | → contacts, → users |
| `activities` | type, notes, scheduled_at, completed | → deals |
| `users` | name, email, role, active | — |

### Roles RBAC

| Rol | Recursos | Acciones | Condición |
|---|---|---|---|
| `admin` | `*` | `*` | — |
| `manager` | `*` | read, create, update | — |
| `sales_rep` | contacts, deals, activities | read, create, update | `owner_id = $user_id` |
| `viewer` | `*` | read | — |

### Hooks pre-configurados

Ninguno por defecto. Añade hooks en `resources.<nombre>.hooks` según tu lógica de negocio.

---

## Personalización

Cada blueprint es un punto de partida. Después de `appximo init --blueprint <name>`:

1. Abre `schema.json` y añade o elimina fields.
2. Ajusta los roles RBAC para tu modelo de negocio.
3. Añade hooks JS para validaciones custom.
4. Ejecuta `appximo validate schema.json` para verificar.
5. Ejecuta `appximo serve --schema schema.json` para arrancar la API.
