import React from 'react';
import ComponentCreator from '@docusaurus/ComponentCreator';

export default [
  {
    path: '/',
    component: ComponentCreator('/', '244'),
    routes: [
      {
        path: '/',
        component: ComponentCreator('/', 'f5f'),
        routes: [
          {
            path: '/',
            component: ComponentCreator('/', 'ee4'),
            routes: [
              {
                path: '/api/API_REFERENCE',
                component: ComponentCreator('/api/API_REFERENCE', 'c6f'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/api/MCP_INTEGRATION',
                component: ComponentCreator('/api/MCP_INTEGRATION', 'f57'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/api/OCR_RESPONSE',
                component: ComponentCreator('/api/OCR_RESPONSE', '273'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/api/v1/docs',
                component: ComponentCreator('/api/v1/docs', '2f3'),
                exact: true
              },
              {
                path: '/guides/onboarding',
                component: ComponentCreator('/guides/onboarding', '598'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/integrations/mezon-bot',
                component: ComponentCreator('/integrations/mezon-bot', '240'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/RELEASE_NOTES',
                component: ComponentCreator('/RELEASE_NOTES', '054'),
                exact: true
              },
              {
                path: '/',
                component: ComponentCreator('/', 'cc0'),
                exact: true,
                sidebar: "docsSidebar"
              }
            ]
          }
        ]
      }
    ]
  },
  {
    path: '*',
    component: ComponentCreator('*'),
  },
];
