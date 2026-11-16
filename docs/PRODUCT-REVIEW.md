---
estado: vigente
dueño: Octavio Santacruz
revisado: 2026-11-16
---

# Revisión de producto: instalación, acceso y primer valor

> **Decisión del dueño (16/11), posterior a esta revisión.** Reemplaza las
> secciones «Propuesta de acceso como producto», «Reformulación propuesta» y
> el MVP Identity/Netskope:
>
> - **Acceso:** login local con usuario y contraseña es el modo base; OIDC se
>   configura desde la consola si hace falta; Ozal es integrable, no impuesto.
> - **MVP:** inventario de herramientas integradas y métricas por herramienta,
>   que crecen a medida que cada empresa habilita APIs y endpoints. No hay
>   población Identity ni elegibilidad de personas en el MVP.
> - **Foco:** organizar y construir integraciones.
>
> El diagnóstico de código, los cortes del onboarding, la navegación que
> excede el backend, la instalación y la reconciliación documental siguen
> siendo válidos. Orden e IDs en [TO_IMPLEMENT.md](TO_IMPLEMENT.md).

Revisión del código y los contratos locales al 16/11. Los hallazgos describen
el estado observado; el MVP y los cambios de prioridad son **propuestas para
el dueño**, no decisiones aceptadas ni funcionalidades entregadas. El
[tracker](TO_IMPLEMENT.md) sigue siendo el único backlog. No se consultaron
datos privados, tenants ni servicios desplegados.

## Diagnóstico

Custodia tiene una demo amplia, una base Go/SQLite con garantías importantes
y recorridos integrados probados. Todavía no tiene un recorrido completo de
producto desde una instalación vacía hasta una conclusión útil sobre dos
herramientas reales, una revisión humana y la operación posterior.

El trabajo pendiente no se reduce a empaquetar ni a agregar un asistente.
Hay que conectar distribución, identidad, permisos, fuentes, modelos de
lectura y seguimiento de una tarea. Ampliar el catálogo antes de cerrar ese
recorrido aumenta la superficie que habrá que sostener.

## Qué existe y qué falta

| Área | Evidencia actual | Distancia al producto |
|---|---|---|
| Presentación | `web/` tiene demo sintética y build contra API; `site/` presenta el caso. | La amplitud de la demo no acredita respaldo de cada pantalla en Go. |
| Persistencia | SQLite, migraciones explícitas, observaciones aisladas, corridas y auditoría. | Faltan recorridos completos de revisión y administración. |
| Personas/cobertura | UI real contra Go; clasificación con contratos Python, permisos y evidencia que envejece. | El recorrido probado usa Identity/Netskope del seed Acme. Falta alimentarlo y seleccionarlo con conexiones reales. |
| Ingesta | Worker, agenda, leases, idempotencia, parciales sin bajas y publicación transaccional. | Falta aceptación con proveedor real y conectar el inventario importado con vistas y reglas del caso de uso. |
| PAM | Adaptador para Safes, miembros, metadatos no secretos de cuentas, plataformas y catálogos PSM; transporte autenticado probado con fixtures TLS. | No hay aceptación de un tenant real ni paridad de las pantallas PAM completas. |
| Alta de fuentes | Formulario en Actividad → Conexiones, alta por grupo/colecciones y probe. | Falta un recorrido inicial reanudable y gestión completa de conexiones existentes. |
| Acceso | Receptor Ozal, bootstrap único, lectores por API, revocación y logout probados. | Falta Ozal real, UI de administración y recuperación; OIDC propio no está implementado. |
| Distribución | Paquete local con binario, frontend, docs y manifiesto; backup/restore y reinicio probados. | Falta una instalación operativa documentada de extremo a extremo, servicio supervisado, upgrade entre versiones y aceptación externa. |

Evidencia: [CLI](../server/cmd/custodia/main.go),
[API de lectura](../server/internal/lab/http.go),
[modelos y selección de conexiones](../server/internal/lab/observations.go),
[alta de conexiones](../server/internal/lab/connections.go),
[registro de conectores](../server/internal/connectors/registry.go),
[paridad y límites](PARITY.md).

## Cortes del recorrido que hay que resolver

1. **Importar no equivale a habilitar el caso de uso.** Las rutas de Personas
   tienen por defecto los IDs Acme. `auth/status` omite las conexiones con
   conector sync al derivar permisos Identity/Netskope. Cobertura exige un
   par con regla registrada; el alta no crea ese contrato. Además, las nuevas
   conexiones conservan internamente el `kind` heredado `netskope`, incluso
   para PAM; el conector es su identidad efectiva. No alcanza con eliminar
   ese filtro o renombrar el campo: faltan adaptación de modelos, selección
   explícita del par, elegibilidad, regla y permisos de todas las entradas.
2. **El onboarding de conexiones es un formulario parcial.** Su resultado y
   el probe viven en estado del componente. No hay allí una lista persistida
   de conexiones para retomar, editar, rotar o retirar. Para recolectar se
   pasa a las otras pestañas. La agenda puede configurarse en el alta sin
   haber pasado el probe; éste no es una condición persistida de activación.
3. **La navegación excede las capacidades del backend.** Inicio ofrece
   CyberArk y Netskope; Go no implementa sus dashboards ni el conjunto de
   endpoints heredados. Los permisos de la UI no son un catálogo de soporte
   del backend. Hace falta derivar ambos y ofrecer estados claros para una
   instalación vacía o un módulo aún no disponible.
4. **Administrar acceso todavía requiere herramientas técnicas.** Crear y
   deshabilitar lectores está implementado, pero no listar/editar su alcance
   desde una pantalla Go completa. Crear un lector no concede `can_run`.
   El comando de concesiones sync está restringido al laboratorio. Falta un
   camino real administrable para delegar operación sin usar al primer admin.
5. **La conclusión no cierra una tarea.** Hay persistencia separada para
   revisiones humanas y pruebas de que el sync no las pisa; faltan API/UI
   para asignar, anotar, resolver y consultar el historial. Exportaciones
   autorizadas también siguen pendientes.

Fuentes adicionales: [formulario de alta](../web/src/cross/components/connection-onboarding.tsx),
[inicio](../web/src/cross/components/home-workspace.tsx),
[administración de acceso](../server/internal/lab/gateway_store.go),
[rutas de ingesta](../server/internal/lab/sync_http.go).

## Cómo funciona el acceso hoy

El camino real disponible es:

```text
Navegador → Ozal autentica y firma cada petición → Custodia valida la firma
          → resuelve issuer + sub → verifica habilitación y concesiones
          → consulta sólo las fuentes autorizadas
```

`init` crea una base vacía, sin usuarios, fuentes ni contraseña. El operador
configura emisor, audiencia, origen público y claves públicas de Ozal; obtiene
el sujeto opaco confirmado y ejecuta `bootstrap-admin` una sola vez. Luego
sirve Go en loopback detrás del gateway. El primer administrador puede entrar
sin conexiones; las que registra por API le conceden lectura y ejecución.
Su rol no concede lectura global sobre las demás conexiones.

Una identidad aceptada por Ozal puede seguir sin acceso a Custodia: un
principal desconocido o deshabilitado recibe 403. Un administrador habilita
lectores con sujetos explícitos y conexiones que él mismo puede leer.
Revocar se aplica en las peticiones siguientes. El logout se delega a Ozal;
Custodia no mantiene una sesión de autenticación paralela.

La firma se exige también para servir la web. Por eso, en ciertos rechazos
previos a cargar la aplicación, la respuesta actual es JSON del backend;
una pantalla React por sí sola no resolverá la experiencia de acceso denegado.
Hay que definir ese estado con el gateway.

`serve-lab` usa bearer sintético en loopback y es otro modo. Los formularios
heredados de login/registro y los tipos OIDC de la UI no significan que Go
ofrezca esos modos. No debe agregarse fallback local para facilitar el alta.
Contrato: [ACCESS.md](ACCESS.md), [middleware](../server/internal/gateway/http.go)
y [vista de autenticación](../web/src/cross/components/auth-view.tsx).

### Propuesta de acceso como producto

- **Alfa asistida:** conservar Ozal y aceptar una instalación real completa.
  Documentar sus prerrequisitos como parte de la instalación, incluida la
  obtención del sujeto del administrador. No anunciar sólo «un binario» si
  el primer ingreso requiere configurar también otro servicio.
- **MVP de distribución independiente:** implementar el cliente OIDC
  genérico ya diseñado en ACCESS.md, para usar el proveedor del equipo sin
  exigir Ozal. Esto propone adelantarlo respecto del orden vigente; Ozal
  conserva su adaptador y no se cambia el modo automáticamente.
- En ambos caminos, Custodia conserva habilitación, permisos y auditoría.
  Resolver alta explícita, listado, cambios de alcance, revocación y
  recuperación administrativa en el host, auditada y sin credenciales
  predeterminadas. No autoasignar administrador al primer visitante.

La identidad para entrar a Custodia y las credenciales para leer CyberArk o
Netskope son configuraciones diferentes. El asistente debe explicarlas por
separado. `secret:` hoy resuelve un archivo en `CUSTODIA_PROBE_SECRET_DIR`;
no es un gestor remoto de secretos. El host todavía debe provisionar esas
referencias y permisos: [resolución](../server/internal/probe/probe.go).

## Reformulación propuesta

**Custodia permite revisar una pregunta de seguridad entre herramientas,
ver la evidencia y conservar lo que decidió el equipo.**

Primer usuario supuesto: un operador de seguridad/infraestructura de un
equipo que ya tiene las fuentes necesarias y hoy las cruza en planillas.
La persona que instala puede ser distinta de quien investiga. Un equipo,
una instalación, SQLite local; sin organizaciones SaaS ni remediación remota.

### MVP recomendado

Una sola pregunta inicial: **«¿Qué personas de la población elegible tienen
equipos con señal reciente en Netskope, y cuáles necesitan revisión?»**

Es el recorrido Identity/Netskope ya contratado en Python. El resultado no
afirma cobertura global de seguridad ni incorpora privilegios PAM sin sus
evidencias. Necesita dos fuentes reales: un origen de población Identity
acotado y Netskope. CyberArk Identity es candidato por la referencia previa;
su contrato debe elegirse y validarse. El conector PAM existente no reemplaza
una población de identidades.

Incluir instalación, ingreso, permisos, dos conectores acotados, elección del
par, elegibilidad explícita, primera ingesta, cobertura con evidencia y
revisión con responsable/estado/nota/historial. Incluir una exportación
autorizada de esa misma población, reinicio, actualización y recuperación.
Conservar los denominadores Python, sin deduplicar por email silenciosamente.

La alfa PAM es un hito técnico útil para probar instalación e ingesta reales.
Por sí sola no demuestra la propuesta de cruces entre herramientas. Si el
primer equipo piloto necesita revisión de accesos PAM en lugar de cobertura,
habrá que elegir ese caso y sus fuentes antes de ampliar implementación;
no intentar entregar los dos MVP a la vez.

Fuera del primer MVP: todo el catálogo de fabricantes, todos los módulos de
DS Hub, motor universal de reglas, constructor libre de dashboards, acciones
remotas, SaaS, billing, alta disponibilidad y migración automática de v1.
La UI existente se conserva; los recorridos disponibles se acotan por soporte
real y permisos. Las capacidades de la demo quedan identificadas como tales.

## Instalación y onboarding objetivo

Propuesta de primer destino soportado: Linux en un solo host, disco local,
binario y web empaquetados, servicio supervisado y TLS mediante una topología
documentada. Fijar distribución/arquitectura al preparar la release. El
usuario final instala artefactos; Node y Go son herramientas de desarrollo.
No exigir Docker y despliegue nativo simultáneamente para aceptar el MVP.

| Paso | Lo que hace el usuario | Criterio de salida |
|---|---|---|
| Preparar host | Sigue una guía única: paquete, usuario de servicio, datos/configuración y TLS. | Verifica versión, integridad, permisos del directorio y proceso saludable. |
| Configurar ingreso | Registra el proveedor/mode explícito y autoriza al administrador. | Login y logout reales; identidad desconocida denegada; recuperación documentada. |
| Abrir Custodia vacía | Ve qué falta y el próximo paso disponible según sus permisos. | Sin métricas demo, pantallas rotas ni ceros que aparenten evaluación. |
| Conectar fuentes | Elige un caso, ve las fuentes/scopes requeridos y provisiona referencias. | Configuración guardada y retomable; probe con error accionable por colección. |
| Recolectar | Confirma la primera extracción y ve progreso, límites y completitud. | Primera corrida por fuente; parcial/fallida/esperando se distinguen de colección vacía. |
| Definir evaluación | Elige población, par de conexiones y regla/ventana soportadas. | Datos nuevos alimentan la vista; no depende de IDs ni reglas del fixture. |
| Investigar | Abre un resultado y sigue sus registros, fechas y regla. | Puede explicar el veredicto, su denominador y sus límites. |
| Revisar y compartir | Asigna responsable, guarda una nota/estado y habilita un lector acotado. | Revisión persistida/auditada; otro usuario ve sólo su alcance; exportación autorizada. |
| Operar | Activa agenda, rota credencial, actualiza y recupera un backup. | Evidencia y decisiones sobreviven; fallos y revocaciones tienen respuesta comprobable. |

Separar «configurada», «acceso probado», «primera corrida completa» y
«evaluación disponible». Persistir los hitos para reanudar tras salir o
reiniciar; derivar disponibilidad de evidencia y permisos actuales. Un probe
correcto no demuestra extracción completa ni todos los permisos por objeto.
Propuesta: dejar la agenda apagada hasta que el operador confirme la primera
recolección; documentar cuándo el asistente contacta APIs externas.

## Orden de trabajo propuesto

Esto es una secuencia de entregas para el backlog existente, no otro tracker.

1. **Cerrar el contrato del MVP.** Elegir primer equipo/caso, origen de
   población, colecciones y permisos. Matriz demo/API Go/soporte por pantalla;
   acotar navegación y reconciliar la documentación contradictoria.
2. **Cerrar la alfa instalable con Ozal.** Guía única y configuración de
   ejemplo incluida en el paquete, comprobación de configuración/salud,
   servicio supervisado, bootstrap, acceso real con dos usuarios y
   recuperación. Aceptar PAM contra un tenant autorizado sin prometer aún el
   caso cruzado. Este hito permite detectar problemas operativos pronto.
3. **Cerrar primer valor.** Adaptadores Identity/Netskope elegidos, conexión
   entre ingesta y modelos de Personas, selección del par, regla y población;
   onboarding persistido hasta un primer resultado explicable.
4. **Cerrar trabajo recurrente y distribución independiente.** Revisión y
   exportación autorizadas, gestión de permisos/credenciales, OIDC genérico,
   diagnóstico de fallos y upgrade real entre dos versiones. No bloquear el
   piloto Ozal esperando OIDC; sí exigirlo para la distribución independiente
   aquí propuesta.
5. **Validar con un equipo ajeno al autor.** Instalar desde cero, resolver la
   tarea y repetirla después de una nueva sincronización sin ayuda manual en
   SQLite. Registrar problemas y volver a priorizar con esa evidencia.

No corresponde estimar un porcentaje restante o prometer fecha de lanzamiento
desde el número de tests. La validación de tenants y el trabajo del primer
equipo piloto todavía no tienen evidencia en esta revisión.

## Aceptación del MVP propuesto

El recorrido debe ejecutarse desde `init`, sin `init-lab`, sin mocks y sin
editar SQLite: instalar → ingresar → conectar dos fuentes autorizadas →
sincronizar → evaluar → revisar → compartir → volver a sincronizar.

Dos usuarios con alcances diferentes verifican que una fuente oculta no se
filtra ni se convierte en una brecha. Probar evidencia vieja, ambigüedad,
parcialidad, cero confirmado, revocación, retry, reinicio y conservación de
decisiones. Aceptar upgrade desde una versión anterior soportada y restore
con configuración/identidad/secretos reprovisionados. Repetir migración del
mismo esquema, como hace el gate actual, no acredita ese upgrade.

Medir tiempo hasta primer resultado útil, pasos que necesitaron ayuda,
comprensión de la evidencia y repetición de una tarea. Un objetivo inicial
propuesto es completar el alta en una sesión con prerrequisitos preparados;
no existe todavía una medición ni un SLA. La evaluación SaaS mantiene las
señales de [VALIDATION.md](VALIDATION.md), no se adelanta por terminar la demo.

## Documentación que necesita reconciliación

- README presenta «interfaz completa» y «adaptadores en curso»: distinguir
  demo, contratos implementados, aceptación real y módulos Go disponibles.
- PRODUCT conserva frases anteriores a Apache 2.0/publicación y al runtime
  actual; la dirección del dueño en AGENTS.md y WEB.md ya las supera.
- ACCESS dice que sync/autorización de jobs está pendiente en general,
  aunque existe `can_run` por conexión; siguen pendientes otras operaciones.
- PARITY y el README del servidor conservan párrafos que describen sólo HTTP
  loopback o backup/distribución pendientes, junto a avances TLS y paquete.
- El backlog conserva nombres/cierres históricos de UI y distribución y
  residuales de verificación. Reconciliar cada estado con evidencia, sin
  convertir la publicación Apache ni un gate sintético en aceptación real.

Estas inconsistencias explican parte de la dificultad de responder «dónde
estamos». Esta revisión no mueve cierres ni modifica contratos de identidad.

## Verificación de esta revisión

Ejecutados y correctos el 16/11/2026:

- `npm run verify`: documentación/enlaces, modelo del experimento y 486
  pruebas unitarias de web.
- `npm run verify:go`: vet, tests con race detector y build.
- `npm run verify:go:gateway`: navegador real, Go/SQLite, aserciones,
  bootstrap, lector acotado, revocación, replay, alta/probe/ingesta PAM de dos
  colecciones con fixture TLS y logout del gateway.
- `npm run verify:go:ops`: paquete temporal, manifiesto, migración repetida,
  backup en línea, restore, reinicio y web/API empaquetadas en modo laboratorio.

Los primeros intentos de Go y verify encontraron restricciones del sandbox
(sockets y streams de subprocesos); las repeticiones fuera de esa restricción
pasaron. Gateway usa un proxy de prueba y un tenant
sintético; ops usa `init-lab`/`serve-lab`. Ninguno demuestra instalación vacía
con un proveedor real, Ozal desplegado o uso externo. No se repitió el gate
amplio `verify:web`; sus residuales siguen registrados en el tracker.
El archivo compartido `/home/saint/projects/AGENTS.md` indicado por las
instrucciones no estaba presente; se aplicaron las instrucciones locales.
