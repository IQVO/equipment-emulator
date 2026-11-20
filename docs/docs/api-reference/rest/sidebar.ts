import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebar: SidebarsConfig = {
  apisidebar: [
    {
      type: "doc",
      id: "api-reference/rest/equipment-emulator-admin-api",
    },
    {
      type: "category",
      label: "Robots",
      link: {
        type: "doc",
        id: "api-reference/rest/robots",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/list-robots",
          label: "List the simulated robots",
          className: "api-method get",
        },
      ],
    },
    {
      type: "category",
      label: "Asrs",
      link: {
        type: "doc",
        id: "api-reference/rest/asrs",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/get-asrs",
          label: "Get an AS/RS snapshot",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/seed-container",
          label: "Seed a container into a storage position",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/place-container-at-port",
          label: "Place a container at a port",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "Faults",
      link: {
        type: "doc",
        id: "api-reference/rest/faults",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/inject-fault",
          label: "Switch a fault on or off",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "Health",
      link: {
        type: "doc",
        id: "api-reference/rest/health",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/healthz",
          label: "Liveness probe",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/readyz",
          label: "Readiness probe",
          className: "api-method get",
        },
      ],
    },
  ],
};

export default sidebar.apisidebar;
