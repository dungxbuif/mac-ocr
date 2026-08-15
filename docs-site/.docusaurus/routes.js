import React from 'react';
import ComponentCreator from '@docusaurus/ComponentCreator';

export default [
  {
    path: '/',
    component: ComponentCreator('/', '4a2'),
    routes: [
      {
        path: '/',
        component: ComponentCreator('/', '342'),
        routes: [
          {
            path: '/',
            component: ComponentCreator('/', '1b6'),
            routes: [
              {
                path: '/api/API_REFERENCE',
                component: ComponentCreator('/api/API_REFERENCE', '386'),
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
                component: ComponentCreator('/api/OCR_RESPONSE', 'e7c'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/api/v1/docs',
                component: ComponentCreator('/api/v1/docs', '2f3'),
                exact: true
              },
              {
                path: '/guides/OMNISCAN_BOT',
                component: ComponentCreator('/guides/OMNISCAN_BOT', 'f10'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/guides/onboarding',
                component: ComponentCreator('/guides/onboarding', '356'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/RELEASE_NOTES',
                component: ComponentCreator('/RELEASE_NOTES', '2c0'),
                exact: true
              },
              {
                path: '/',
                component: ComponentCreator('/', '51b'),
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
