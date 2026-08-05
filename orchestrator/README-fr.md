# SAGE Resilient Urgent Scheduler

[English version](README.md)

Dépôt de référence :
[github.com/NunesClement/sage-resilient-urgent-scheduler](https://github.com/NunesClement/sage-resilient-urgent-scheduler)

Ce dépôt contient un premier MVP d’ordonnancement explicable pour les nœuds SAGE/Waggle. Il classe les applications urgentes, applique des limites simples de capacité et fournit un adaptateur compilable avec le `NodeScheduler` SAGE.

Le cœur de la politique est indépendant de l’application : les adaptateurs lui
fournissent des tâches et des capacités génériques, tandis que l’acquisition,
l’inférence et le transport restent hors du scheduler. Le projet Summer Camp
branche ce cœur sur Mortimus via l’adaptateur SAGE ; d’autres applications ou
contrôleurs peuvent réutiliser le même moteur avec leur propre adaptateur.

Le MVP n’a été ni déployé ni connecté à un nœud SAGE. Le moteur peut être utilisé hors ligne avec `policyctl`, tandis que le module `integrations/sage` prépare l’intégration réelle sans embarquer une copie du dépôt SAGE.

### Ce que fera l’orchestrateur

1. Lire une politique validée et un instantané des tâches prêtes et actives.

2. Donner à chaque tâche un score fondé sur la priorité, la marge avant échéance, l’ancienneté et la fiabilité prévue.

3. Favoriser les tâches urgentes tout en vieillissant les tâches anciennes pour limiter la famine.

4. Différer les tâches qui dépassent les limites de concurrence, de GPU, de fiabilité ou, lorsque l’information est fiable, de ressources.

5. Retourner une décision déterministe et expliquée sans modifier directement les files SAGE.

6. Utiliser un repli borné suivant l’ordre de la file si le moteur échoue et que le mode `failOpen` est activé.

7. Laisser le `NodeScheduler` SAGE existant créer et suivre les Pods après la sélection.

8. Coordonner en option une caméra principale et une source d’image
   additionnelle HaLow (ou autre), puis fournir une paire de deux images validée
   à l’application d’IA sélectionnée.

---

### Étapes de développement

1. Implémenter et tester le moteur de décision indépendant de SAGE et Kubernetes.

2. Fournir une configuration YAML stricte et un outil hors ligne pour rejouer des scénarios.

3. Adapter les files et les types de `edge-scheduler` à l’interface du moteur.

4. Fournir un binaire `waggle-nodescheduler` qui injecte la nouvelle politique dans le constructeur SAGE existant.

5. Valider la configuration hors ligne, sans contacter Kubernetes, RabbitMQ ou un nœud SAGE.

6. Confirmer avec l’équipe SAGE la version de WES/k3s et le mode de déploiement attendu.

7. Tester ensuite sur KWOK, puis sur k3s, puis en mode shadow et canary sur du matériel SAGE.

---

### Ce que le MVP fait réellement

- Classe les tâches par priorité, *slack*, ancienneté et probabilité de succès.
- Signale les échéances probablement impossibles.
- Limite le nombre total de tâches sélectionnées et le nombre de tâches GPU.
- Peut filtrer selon CPU, mémoire et GPU lorsque la capacité fournie est réelle.
- Rejette les métadonnées invalides et explique chaque sélection ou report.
- Propose un repli borné en cas d’erreur interne.
- Ignore par défaut les hints fournis par les workloads ; leur usage exige une
  activation explicite dans un pilote de confiance.
- Compile contre l’interface `SchedulingPolicy` du commit SAGE inspecté.
- Fournit un cœur optionnel pour coordonner deux caméras, avec un contrat
  requête/réponse HaLow versionné, une position GPS/arpentée et un état PTZ
  associés à chaque prise de vue, ainsi qu’un transfert découpé vérifiable ; le
  comportement existant du scheduler ne change pas.
- Fournit une passerelle optionnelle `intentctl` qui demande au service
  Hermes/GLM existant de traduire le langage naturel en un petit brouillon
  d’objectif scientifique utilisant les termes SAGE existants.

### Ce que le MVP ne fait pas encore

- Il ne préempte pas un Pod déjà actif.
- Il n’exécute pas de retry et ne gère pas de budget de reprises.
- Il ne crée ni checkpoint, ni réplica, ni migration.
- Il ne lance pas automatiquement une application alternative : le « fallback » actuel est un repli de décision, pas une application de secours.
- Il ne persiste pas l’âge des tâches au redémarrage.
- Il n’utilise pas encore les capteurs, l’énergie, la température ou une prédiction apprise.
- Il ne remplace pas les garanties temps réel absentes de Kubernetes.
- Il ne fournit pas encore d’autorisation multi-tenant des niveaux d’urgence.
- Un brouillon d’intention n’est pas exécutable : le traducteur ne choisit pas de
  plugin, n’attribue pas de priorité, ne soumet pas de job SAGE et ne contourne
  pas l’approbation humaine.
- Il ne fournit pas d’adaptateur MQTT/Meshtastic concret, de pilote matériel
  pour caméra, GPS ou PTZ, ni d’adaptateur d’IA. Ces éléments dépendent du
  déploiement et implémentent les interfaces du cœur.
- Le commit Waggle épinglé retire les files par nom de plugin seulement.
  L’adaptateur sérialise les homonymes sur le chemin de sélection, mais une
  correction amont par identité complète reste obligatoire avant un canary.
- La file Waggle épinglée n’offre pas de snapshot atomique : son API REST peut
  ajouter une tâche pendant l’itération de la politique. Une méthode de
  snapshot verrouillée ou une soumission sérialisée par channel est également
  requise en amont avant un canary.
- Le `POST /api/v1/schedule` local de cette révision Waggle ne prépare pas
  complètement le runtime SAGE. Il doit rester inaccessible et inutilisé
  jusqu’à sa correction amont ; le chemin normal par goals/science rules reste
  celui visé par le MVP.

## Réponses aux trois questions

### 1. Comment piloter SAGE avec résilience et urgence ?

On conserve SAGE comme orchestrateur et on remplace uniquement sa politique de sélection. Chaque application peut fournir une priorité, une durée estimée, une échéance ou une latence maximale, et éventuellement une probabilité de succès. Ces déclarations ne sont acceptées que dans un pilote de confiance ; un environnement multi-utilisateur devra les autoriser et les borner côté cloud. Le moteur classe alors les applications et n’en retourne qu’un nombre compatible avec les limites configurées.

Cette première étape apporte une admission et un ordre explicables. La résilience complète nécessitera ensuite des actions supplémentaires dans le contrôleur SAGE : reprise, checkpoint, réplication, préemption bornée et persistance d’état.

### 2. Comment utiliser le chaos pour étudier les politiques ?

`policyctl` permet de rejouer exactement le même instantané avec des variations contrôlées : charge, capacité GPU, échéance, durée estimée ou fiabilité. `chaoslab` mesure la sensibilité des sélections, notamment avec leur distance de Jaccard, tandis que KWOK pourra injecter des pannes et transitions Kubernetes à grande échelle.

Ce dépôt ne prétend pas encore injecter ces pannes. Il fournit le moteur déterministe et les décisions détaillées nécessaires à un futur banc d’expérimentation reproductible.

### 3. Comment implémenter et valider un agent autonome à l’edge ?

L’agent d’ordonnancement commence ici comme une boucle locale déterministe :
observer les files, décider, expliquer, puis laisser SAGE appliquer la
sélection. Il n’appelle aucun service d’IA et ne reçoit pas de droit Kubernetes
supplémentaire. Séparément, le cœur optionnel à deux caméras appelle uniquement
un `PairAnalyzer` injecté ; ce dépôt ne contient aucune implémentation de
service d’IA.

La validation doit progresser par tests unitaires et fuzzing, replay hors ligne, KWOK, k3s avec fautes contrôlées, mode shadow sur SAGE, puis canary sur quelques nœuds. Les actions destructrices ou autonomes resteront derrière des garde-fous déterministes.

## Utilisation hors ligne

Valider uniquement la configuration :

```bash
go run ./cmd/policyctl \
  -config config/policy.example.yaml \
  -validate-config
```

Calculer une décision depuis un instantané JSON :

```bash
go run ./cmd/policyctl \
  -config config/policy.example.yaml \
  -snapshot examples/snapshots/urgent-vs-routine.json
```

Mesurer la sensibilité à de petites perturbations :

```bash
go run ./cmd/chaoslab \
  -config config/policy.example.yaml \
  -experiment examples/chaos/sensitivity.json
```

Valider le binaire SAGE sans établir de connexion :

```bash
go run ./integrations/sage/cmd/waggle-nodescheduler \
  -policy-config config/policy.example.yaml \
  -validate-config
```

La dernière commande initialise la configuration runtime Waggle par défaut,
charge la politique, puis s’arrête avant `scheduler.Configure()` : elle ne
contacte ni k3s, ni RabbitMQ, ni les services WES.

La configuration d’exemple laisse `trustWorkloadEnvHints` à `false`. Un
opérateur peut l’activer pour un pilote fermé après avoir vérifié qui peut
soumettre des hints ; en environnement partagé, une admission SAGE doit les
autoriser selon l’identité du demandeur.

## Traduction optionnelle des intentions

`intentctl` utilise le service Hermes interne existant via une API de
chat-completions compatible OpenAI. GLM 5.2 est le modèle par défaut ; un
déploiement de modèle séparé n’est pas nécessaire pour cette première version.

```bash
export HERMES_CHAT_COMPLETIONS_URL=http://hermes.internal/v1/chat/completions
export HERMES_API_KEY=a-remplacer-si-necessaire

go run ./cmd/intentctl -input examples/intents/cloud-cover.txt
```

Le résultat est un petit brouillon JSON contenant `goal`, `applications`,
`nodes`, `nodeTags`, `scienceRules`, `successCriteria` et les `questions`
ouvertes. Il porte `humanApprovalRequired: true` et ne peut pas être transmis
directement au scheduler. Si Hermes ne gère pas le mode de réponse JSON
d’OpenAI, ajouter `-json-mode=false`. Voir
[docs/intent-translation.md](docs/intent-translation.md) pour le mapping.

## Structure

- `pkg/policy` : moteur déterministe et types indépendants.
- `pkg/orchestration` : coordination optionnelle d’une caméra principale et
  d’une caméra additionnelle, avec une interface d’IA générique.
- `pkg/intent` : petit brouillon d’objectif scientifique et frontière de
  traduction Hermes.
- `cmd/policyctl` : validation et replay hors ligne.
- `cmd/chaoslab` : étude hors ligne de la sensibilité des décisions.
- `cmd/intentctl` : traduction optionnelle du langage naturel vers un brouillon
  d’objectif scientifique.
- `integrations/sage/adapter` : conversion des files Waggle vers le moteur.
- `integrations/sage/cmd/waggle-nodescheduler` : binaire SAGE avec la politique injectée.
- `integrations/sage/deploy` : manifests préparés, non appliqués, et procédure de redémarrage.
- `docs/architecture-fr.md` : calcul de décision, limites et trajectoire de validation.
- `docs/halow-orchestration.md` : flux additionnel à deux images et contrat du
  protocole HaLow.
- `docs/intent-translation.md` : sémantique des intentions, frontière du
  modèle et utilisation.
- `docs/sage-integration-fr.md` : contrat précis avec SAGE et question de déploiement.

Le code reste testé avec Go 1.20 pour la compatibilité de la dépendance SAGE.
L’image est compilée avec Go 1.26.5 afin d’embarquer une bibliothèque standard
maintenue.

## Sources et pistes de recherche

1. [Waggle edge-scheduler](https://github.com/waggle-sensor/edge-scheduler),
   ses [politiques NodeScheduler](https://github.com/waggle-sensor/edge-scheduler/tree/main/pkg/nodescheduler/policy)
   et l’[argument `policy`](https://github.com/waggle-sensor/edge-scheduler/blob/main/cmd/nodescheduler/main.go#L46).
   Les politiques Waggle sont écrites en Go et compilées dans le binaire du
   scheduler. Une logique écrite en Python ou dans un autre langage
   nécessiterait un pont compilé.

2. [Kubernetes Scheduler](https://kubernetes.io/docs/concepts/scheduling-eviction/kube-scheduler/)
   et [Kubernetes scheduler-plugins](https://github.com/kubernetes-sigs/scheduler-plugins).
   Le `NodeScheduler` Waggle crée des Pods dont le placement final est géré par
   le scheduler du cluster. Un scheduler personnalisé ou un plugin du
   Scheduling Framework constitue une autre couche d’implémentation possible,
   sous réserve d’une compatibilité exacte avec la version de k3s/Kubernetes.

3. [KWOK](https://kwok.sigs.k8s.io/) peut simuler des nœuds et des Pods
   Kubernetes afin de tester le scheduler à l’échelle du plan de contrôle. Il
   complète, mais ne remplace pas, les tests avec de vrais conteneurs, GPU,
   capteurs et services SAGE.

4. [Le profil Google Scholar de Michael Wooldridge](https://scholar.google.com/citations?user=JD8v9fkAAAAJ&hl=en&oi=sra)
   constitue une piste de recherche sur l’ordonnancement prédictif des tâches
   et les travaux connexes en systèmes multi-agents. Aucun article précis de ce
   profil sur l’ordonnancement prédictif temps réel n’a encore été identifié
   pour le projet.

## Licence

Le code propre à ce dépôt est fourni sous licence Apache-2.0. La dépendance `waggle-sensor/edge-scheduler` demande une vérification séparée avant toute distribution binaire ; voir [NOTICE](NOTICE).
