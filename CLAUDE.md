# CLAUDE

Notes for the agent:

I really appreciate your cooperation, but follow the rules

## Important: 
- DO NOT run kubectl locally! 
- DO NOT connect to the kubernetes cluster!
- DO NOT push docker imager from this machine!
- DO NOT push helm releases from this machine!
- DO NOT apply any terraform or other IaC locally!

## Requirements
- follow hexagonal architecture
- split cross-cutting concerns from business code
- write doc comments for public fields and functions
- when adding or editing endpoints update the swagger annotations to generate the swagger doc
- put reusable code (especially cross-cutting concerns like authz, authn) into pkg directory