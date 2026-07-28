# Intégration SAGE/Waggle

[English version](sage-integration.md)

## Révision ciblée

L’adaptateur a été préparé à partir de :

- dépôt : [waggle-sensor/edge-scheduler](https://github.com/waggle-sensor/edge-scheduler) ;
- commit inspecté : [`5391a00b34fa069f14b4ce50153725571007b5ef`](https://github.com/waggle-sensor/edge-scheduler/commit/5391a00b34fa069f14b4ce50153725571007b5ef) ;
- interface : [`pkg/nodescheduler/policy`](https://github.com/waggle-sensor/edge-scheduler/tree/5391a00b34fa069f14b4ce50153725571007b5ef/pkg/nodescheduler/policy).

Le module d’intégration épingle cette révision. Il doit être recompilé et revalidé si la version installée sur SAGE diffère.

## Pourquoi une intégration compilée ?

Le `NodeScheduler` choisit une politique par son argument `policy`, mais le registre amont ne connaît que les politiques compilées dans son binaire. Il n’existe pas de chargement dynamique d’un script Python.

Le MVP fournit donc un second point d’entrée Go qui :

1. charge la configuration Waggle habituelle ;
2. charge la configuration `ResilientUrgentPolicy` ;
3. construit les composants amont avec `NewNodeSchedulerBuilder` ;
4. affecte l’adaptateur au champ `SchedulingPolicy` ;
5. lance ensuite le même `NodeScheduler` lorsque le mode de validation n’est pas demandé.

L’arbre source amont n’est ni vendored ni modifié dans ce dépôt ; l’intégration le référence comme module Go épinglé.

## Validation sans connexion

Depuis la racine du dépôt :

```bash
go run ./integrations/sage/cmd/waggle-nodescheduler \
  -policy-config config/policy.example.yaml \
  -validate-config
```

Cette commande :

- vérifie la configuration de politique ;
- vérifie que le binaire d’intégration se construit contre l’API épinglée ;
- s’arrête avant `scheduler.Configure()`.

Elle ne contacte pas :

- l’API k3s ;
- RabbitMQ ;
- le science-rule checker ;
- le scoreboard ;
- le cloud scheduler.

Ne pas exécuter le binaire sans `-validate-config` sur une machine reliée à SAGE tant que le déploiement n’a pas été validé avec l’équipe.

## Métadonnées d’ordonnancement

Le type `PluginRuntime` amont ne contient ni priorité, ni échéance. En attendant une évolution du schéma SAGE, l’adaptateur lit des hints dans `pluginSpec.env`.

`PluginSpec.Env` est contrôlé par l’auteur du job et transmis au conteneur :
ce n’est pas une source attestée. `trustWorkloadEnvHints` vaut donc `false`
par défaut, y compris dans la configuration et la ConfigMap d’exemple. Un
opérateur peut l’activer pour un pilote fermé. Avant un usage multi-tenant,
une admission cloud devra autoriser, borner et auditer ces propriétés selon
l’identité du demandeur.

| Variable | Format | Défaut |
|---|---|---|
| `SAGE_SCHEDULER_PRIORITY` | entier de 0 à 100 | `defaultPriority` |
| `SAGE_SCHEDULER_MAX_LATENCY` | durée Go positive, par exemple `2m` | `defaultMaxQueueLatency` |
| `SAGE_SCHEDULER_ESTIMATED_RUNTIME` | durée Go positive | `defaultEstimatedRuntime` |
| `SAGE_SCHEDULER_DEADLINE_AT` | date RFC3339 | échéance calculée depuis la latence maximale |
| `SAGE_SCHEDULER_SUCCESS_RATE` | nombre entre 0 et 1 | `defaultSuccessRate` |

Exemple de plugin :

```yaml
plugins:
  - name: smoke-detector
    pluginSpec:
      image: registry.sagecontinuum.org/example/smoke-detector:1.0.0
      env:
        SAGE_SCHEDULER_PRIORITY: "95"
        SAGE_SCHEDULER_MAX_LATENCY: "30s"
        SAGE_SCHEDULER_ESTIMATED_RUNTIME: "12s"
        SAGE_SCHEDULER_SUCCESS_RATE: "0.92"
      selector:
        resource.gpu: "true"
      resource:
        request.cpu: "500m"
        request.memory: "512Mi"
        limit.gpu: "1"
```

Pour une règle périodique, préférer `SAGE_SCHEDULER_MAX_LATENCY`. Une échéance RFC3339 fixe devient rapidement périmée si le même plugin est déclenché plusieurs fois.

Ces variables sont également transmises au conteneur applicatif par SAGE. Une intégration amont propre devra plutôt ajouter ces propriétés aux paramètres de `schedule(...)`, déjà analysés par `ScienceRule.ActionParameters`, puis les conserver dans `PluginRuntime`.

Ne pas stocker les hints dans :

- `pluginSpec.selector`, car chaque clé devient un `nodeSelector` Kubernetes ;
- `pluginSpec.resource`, car une clé inconnue devient une ressource Kubernetes étendue.

## GPU et ressources

Une tâche est considérée GPU si l’un des éléments suivants est positif :

- `selector.resource.gpu: "true"` ;
- `resource.limit.gpu` ;
- `resource.nvidia.com/gpu`.

`maxGPUConcurrent` est une limite logique fondée sur les files SAGE. Elle ne prouve ni la présence physique d’un GPU, ni sa mémoire libre.
Elle compte des workloads GPU, pas des unités GPU. Les demandes de plusieurs
GPU restent donc hors du mode d’ajustement de capacité de cet adaptateur tant
qu’une capacité GPU réelle n’est pas fournie par Waggle.

Toutes les quantités déclarées sont validées avant sélection. Les clés SAGE
connues sont `request.cpu`, `limit.cpu`, `request.memory`, `limit.memory` et
`limit.gpu`. Une autre clé doit être une ressource étendue qualifiée, par
exemple `example.com/fpga`, avec une quantité entière. Une request CPU ou
mémoire ne peut pas dépasser sa limite, et les alias GPU simultanés doivent
avoir la même valeur.

Le commit amont inspecté transmet actuellement à chaque politique une capacité fictive très élevée. Pour cette raison :

- `enforceResourceFit` doit rester désactivé dans le premier déploiement ;
- Kubernetes continue d’appliquer les vraies `requests` et `limits` ;
- un changement amont sera nécessaire pour fournir une capacité instantanée fiable au moteur.

## Sémantique des files

L’adaptateur :

- photographie `readyQueue` et `scheduledQueue` ;
- ne les modifie jamais ;
- retourne des pointeurs provenant de `readyQueue` ;
- limite le nombre retourné selon la configuration.

Le `NodeScheduler` amont reste responsable du retrait de la file, du passage dans `scheduledQueue`, de la création du Pod et des événements de cycle de vie.

L’identité interne concatène `GoalID`, `JobID`, le nom du plugin et `PodInstance`. L’âge est mémorisé à la première observation et n’est pas persistant.

L’adaptateur ne lit volontairement pas `PluginSpec.Job` pour décider : le
contrôleur Waggle épinglé réécrit ce champ dans une goroutine après création
du Pod, sans verrou partagé avec la politique. Les propriétés utilisées par la
décision sont limitées aux métadonnées stables et aux déclarations du spec.

Limite amont bloquante : `Queue.Pop` compare seulement `Plugin.Name`, y compris
sur certains chemins de nettoyage de goals. L’adaptateur sérialise les noms
identiques entre `readyQueue` et `scheduledQueue` pour sécuriser sa propre
sélection. Un premier homonyme invalide peut donc bloquer les suivants, et le
nettoyage amont reste susceptible de retirer le mauvais runtime. Il faut
corriger Waggle pour supprimer par pointeur ou identité complète, puis ajouter
une quarantaine/requeue, avant un canary.

Deuxième limite amont bloquante : la `Queue` épinglée n’expose pas de snapshot
atomique. `Length` et `More` lisent son état sans verrou tandis que l’API REST
peut appeler `Push` depuis une autre goroutine. Le mutex interne de
l’adaptateur ne protège donc pas cette course. Waggle doit ajouter une copie
atomique sous le verrou de la file, ou sérialiser la soumission REST via le
channel de la boucle principale, avant un canary.

Le `POST /api/v1/schedule` de la même révision crée aussi un runtime sans
transition `Queued`, sans `PodInstance` et sans enregistrement dans le
`GoalManager`. Ce chemin local est donc explicitement non supporté par le MVP :
il doit rester non exposé et ne pas être appelé. Le chemin normal
goals/science rules est le seul prévu jusqu’à une correction amont.

## Modes d’échec

### Hint invalide

Une tâche contenant une priorité, une durée, une date ou une probabilité invalide est rejetée par la décision. L’erreur est journalisée. Le MVP ne publie pas encore cette raison dans les événements SAGE.

### Erreur du moteur

Avec `failOpen: true`, l’adaptateur revient à l’ordre de la file en respectant les limites globale, GPU et les collisions de nom. La décision est enregistrée avec `fail_open_fallback`. Avec `failOpen: false`, il renvoie l’erreur au `NodeScheduler`.

### Échec de création du Pod

La politique valide les hints, les noms et toutes les quantités de ressources
qu’elle voit. D’autres erreurs restent possibles dans le contrôleur amont.
Dans le commit épinglé, un échec après déplacement vers `scheduledPlugins`
n’est pas toujours requeue ; avec une concurrence de 1, cela peut bloquer
l’admission. Un watchdog/requeue amont est requis pour la résilience réelle.

### Modes Waggle non sûrs

Le champ amont `NoRabbitMQ` n’empêche pas le builder de créer le transport, et
le mode `Simulate` peut continuer jusqu’à un client Kubernetes absent. Le
binaire accepte ces valeurs pour `-validate-config`, mais les refuse avant
toute exécution réelle.

Les valeurs de compatibilité RabbitMQ `service/service` sont héritées du
binaire Waggle, mais refusées en exécution réelle par défaut. Le déploiement
doit conserver des variables `RABBITMQ_USERNAME` et `RABBITMQ_PASSWORD`
provenant d’un Secret. Le flag
`--allow-insecure-rabbitmq-defaults` exige un acquittement explicite si un
ancien site dépend encore de ces valeurs.

### Échéance impossible

La tâche reçoit `predictedDeadlineMiss: true`, mais peut rester sélectionnée. Le MVP ne connaît ni version dégradée ni application alternative à lancer.

### Redémarrage

L’ancienneté locale est perdue. De plus, la révision amont inspectée nettoie les Pods applicatifs au démarrage de son `ResourceManager`. La reprise après redémarrage doit donc être conçue dans SAGE avant de qualifier le système de résilient.

## Ce que l’interface de politique ne permet pas

La méthode `SelectBestPlugins` peut sélectionner ou différer, mais elle ne peut pas :

- supprimer ou préempter un Pod actif ;
- déclencher un checkpoint ;
- créer une réplique ;
- migrer une tâche ;
- gérer un retry ;
- réserver une ressource Kubernetes ;
- modifier une `PriorityClass`.

Ces capacités exigeront une extension du contrôleur SAGE ou un plugin Kubernetes versionné avec le k3s réellement déployé.

## Déploiement futur

Avant tout déploiement :

1. confirmer l’image et le commit exacts de `edge-scheduler` sur les nœuds ;
2. relever `k3s version` et les architectures cibles, notamment `linux/arm64` ;
3. choisir entre une image `NodeScheduler` de remplacement ou l’ajout amont de `resilient-urgent` au registre `--policy` ;
4. confirmer le chemin de montage de la politique et le compte de service WES ;
5. générer un hash de la ConfigMap dans le Pod template, car la politique est
   chargée uniquement au démarrage ;
6. corriger les opérations `Queue.Pop`/nettoyage par identité complète et le
   requeue après échec de création ;
7. rendre la photographie des files atomique face aux soumissions de l’API
   REST ;
8. désactiver ou corriger `POST /api/v1/schedule`, et vérifier l’exposition
   réseau du port 8080 ;
9. confirmer la licence de `edge-scheduler`, conformément à [NOTICE](../NOTICE) ;
10. valider hors ligne, puis en shadow, avant un canary.

Les manifests préparés et la procédure de redémarrage contrôlé sont décrits
dans [`integrations/sage/deploy`](../integrations/sage/deploy/README-fr.md).

### Question à l’équipe SAGE

**Souhaitez-vous que la première intégration soit livrée comme une image de remplacement du `NodeScheduler` WES, ou comme une contribution amont ajoutant `resilient-urgent` au registre existant de l’argument `--policy` ?**

Merci de fournir également la version exacte de l’image `waggle/scheduler` et la sortie de `k3s version` du nœud cible. Ce choix détermine le packaging, le manifeste et la compatibilité Kubernetes ; il n’est volontairement pas supposé dans ce MVP.

Le module garde `go 1.20` et la CI vérifie cette compatibilité source, mais
l’image statique est construite avec Go 1.26.5 pour recevoir les correctifs
récents de la bibliothèque standard.

## Références

- [Architecture SAGE](https://sagecontinuum.org/docs/about/architecture)
- [Waggle edge-scheduler](https://github.com/waggle-sensor/edge-scheduler)
- [Politiques Waggle](https://github.com/waggle-sensor/edge-scheduler/tree/main/pkg/nodescheduler/policy)
- [Kubernetes Scheduling Framework](https://kubernetes.io/docs/concepts/scheduling-eviction/scheduling-framework/)
- [scheduler-plugins et matrice de compatibilité](https://github.com/kubernetes-sigs/scheduler-plugins#compatibility-matrix)
- [KWOK](https://kwok.sigs.k8s.io/)
